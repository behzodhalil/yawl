package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type CA interface {
	Verify(c *x509.Certificate) error
	Sign(csr *x509.CertificateRequest, serverID string) ([]byte, error)
}

type agentConn struct {
	ws     *websocket.Conn
	wmu    sync.Mutex
	closed chan struct{}
	cancel context.CancelFunc
	serial string
}

type Gateway struct {
	PingEvery time.Duration
	Version   string

	q   *db.Queries
	ca  CA
	log *slog.Logger

	mu      sync.Mutex
	conns   map[string]*agentConn
	changed chan struct{}
	pending map[string]chan proto.Result
	terms   map[string]*Term
}

func New(q *db.Queries, ca CA, log *slog.Logger) *Gateway {
	if log == nil {
		log = slog.Default()
	}
	return &Gateway{PingEvery: 10 * time.Second, q: q, ca: ca, log: log, conns: map[string]*agentConn{},
		changed: make(chan struct{}), pending: map[string]chan proto.Result{}}
}

// Close drops every agent connection; agents reconnect with backoff to
// whatever serves next. http.Server.Shutdown does not end hijacked WebSockets.
func (g *Gateway) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, c := range g.conns {
		c.cancel()
		delete(g.conns, id)
	}
	g.signalLocked()
}

func (g *Gateway) Connected(serverID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.conns[serverID]
	return ok
}

func (g *Gateway) Drop(serverID string) {
	g.mu.Lock()
	c := g.conns[serverID]
	g.mu.Unlock()
	if c != nil {
		c.cancel()
	}
}

func pendingKey(serverID, key string) string { return serverID + "\x00" + key }

func (g *Gateway) Send(ctx context.Context, serverID string, cmd proto.Command) (proto.Result, error) {
	if cmd.Key == "" {
		return proto.Result{}, errors.New("a command needs a key")
	}
	ch, pk := make(chan proto.Result, 1), pendingKey(serverID, cmd.Key)
	g.mu.Lock()
	g.pending[pk] = ch
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, pk)
		g.mu.Unlock()
	}()
	msg, err := proto.Encode(proto.TypeCommand, cmd.Key, cmd)
	if err != nil {
		return proto.Result{}, err
	}
	for {
		g.mu.Lock()
		c, changed := g.conns[serverID], g.changed
		g.mu.Unlock()
		if c == nil {
			select {
			case <-ctx.Done():
				return proto.Result{}, errors.Join(errors.New("agent not connected"), ctx.Err())
			case <-changed:
				continue
			}
		}
		c.wmu.Lock()
		err := c.ws.Write(ctx, websocket.MessageText, msg)
		c.wmu.Unlock()
		if err != nil {
			select {
			case <-ctx.Done():
				return proto.Result{}, ctx.Err()
			case <-c.closed:
				continue
			}
		}
		select {
		case res := <-ch:
			return res, nil
		case <-c.closed:
			continue
		case <-ctx.Done():
			return proto.Result{}, ctx.Err()
		}
	}
}

func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		id, version, serial, err := g.authenticate(ctx, ws)
		if err != nil {
			g.log.Warn("agent refused", "err", err, "remote", r.RemoteAddr)
			ws.Close(websocket.StatusPolicyViolation, "not authenticated")
			return
		}
		ws.SetReadLimit(agentReadLimit)
		c := &agentConn{ws: ws, closed: make(chan struct{}), cancel: cancel, serial: serial}
		g.swap(id, c)
		defer func() {
			g.mu.Lock()
			if g.conns[id] == c {
				delete(g.conns, id)
			}
			g.signalLocked()
			g.mu.Unlock()
			close(c.closed)
			g.dropTerms(id, c)
		}()
		if srv, err := g.q.GetServer(ctx, id); err != nil || srv.CertSerial != serial {
			g.log.Warn("agent refused", "server", id, "err", "certificate was replaced by a newer one")
			return
		}
		g.log.Info("agent connected", "server", id, "version", version)
		go keepalive(ctx, ws, g.PingEvery, cancel)
		for {
			_, b, err := ws.Read(ctx)
			if err != nil {
				return
			}
			env, err := proto.Decode(b)
			if err != nil {
				return
			}
			switch env.Type {
			case proto.TypeResult:
				var res proto.Result
				if json.Unmarshal(env.Body, &res) == nil {
					g.mu.Lock()
					ch := g.pending[pendingKey(id, res.Key)]
					g.mu.Unlock()
					if ch != nil {
						select {
						case ch <- res:
						default:
						}
					}
				}
			case proto.TypeTermOut, proto.TypeTermEnd:
				g.deliverTerm(id, env)
			case proto.TypeRenew:
				if err := g.renew(ctx, c, id, env.Body); err != nil {
					g.log.Warn("certificate renewal refused", "server", id, "err", err)
				}
			case proto.TypeHeartbeat:
				var hb proto.Heartbeat
				if json.Unmarshal(env.Body, &hb) == nil {
					now := time.Now().UnixMilli()
					var booted int64
					if hb.UptimeS > 0 {
						booted = now - hb.UptimeS*1000
					}
					g.q.TouchServer(ctx, db.TouchServerParams{
						LastHeartbeat: sql.NullInt64{Int64: now, Valid: true}, AgentVersion: version,
						DiskUsedPct: int64(hb.DiskUsedPct), OsName: hb.OSName, OsID: hb.OSID, Kernel: hb.Kernel, Cores: int64(hb.Cores), BootedAt: booted,
						UpdatedAt: now, ID: id,
					})
				}
			}
		}
	})
}

func (g *Gateway) swap(id string, c *agentConn) {
	g.mu.Lock()
	old := g.conns[id]
	g.conns[id] = c
	g.signalLocked()
	g.mu.Unlock()
	if old != nil {
		old.cancel()
	}
}

func (g *Gateway) signalLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func (g *Gateway) challenge(nonce []byte) []byte {
	b, _ := proto.Encode(proto.TypeChallenge, "", proto.Challenge{Nonce: nonce, ServerVersion: g.Version})
	return b
}

const (
	preHelloReadLimit = 32768
	agentReadLimit    = 4 << 20
)

func (g *Gateway) authenticate(ctx context.Context, ws *websocket.Conn) (id, version, serial string, err error) {
	ws.SetReadLimit(preHelloReadLimit)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	nonce := make([]byte, 32)
	rand.Read(nonce)
	if err := ws.Write(ctx, websocket.MessageText, g.challenge(nonce)); err != nil {
		return "", "", "", err
	}
	_, b, err := ws.Read(ctx)
	if err != nil {
		return "", "", "", err
	}
	env, err := proto.Decode(b)
	if err != nil {
		return "", "", "", err
	}
	var h proto.Hello
	if env.Type != proto.TypeHello || json.Unmarshal(env.Body, &h) != nil {
		return "", "", "", errors.New("expected hello")
	}
	blk, _ := pem.Decode([]byte(h.CertPEM))
	if blk == nil {
		return "", "", "", errors.New("hello carries no certificate")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return "", "", "", err
	}
	if err := g.ca.Verify(cert); err != nil {
		return "", "", "", err
	}
	if cert.Subject.CommonName != h.ServerID {
		return "", "", "", errors.New("certificate is for another server")
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	sum := sha256.Sum256(proto.AuthMessage(nonce))
	if !ok || !ecdsa.VerifyASN1(pub, sum[:], h.Sig) {
		return "", "", "", errors.New("bad signature")
	}
	srv, err := g.q.GetServer(ctx, h.ServerID)
	if err != nil {
		return "", "", "", errors.New("unknown server")
	}
	serial = cert.SerialNumber.Text(16)
	switch {
	case srv.CertSerial != "" && serial == srv.CertSerial:
	case srv.NextCertSerial != "" && serial == srv.NextCertSerial:
		if n, err := g.q.PromoteServerCert(ctx, db.PromoteServerCertParams{UpdatedAt: time.Now().UnixMilli(), ID: srv.ID, NextCertSerial: serial}); err != nil || n != 1 {
			return "", "", "", errors.New("could not accept the renewed certificate")
		}
	default:
		return "", "", "", errors.New("certificate was replaced by a newer one")
	}
	return h.ServerID, h.AgentVersion, serial, nil
}

func (g *Gateway) renew(ctx context.Context, c *agentConn, id string, body json.RawMessage) error {
	var req proto.Renew
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	blk, _ := pem.Decode([]byte(req.CSRPEM))
	if blk == nil {
		return errors.New("renew carries no CSR")
	}
	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		return err
	}
	if srv, err := g.q.GetServer(ctx, id); err != nil || srv.CertSerial != c.serial {
		c.cancel()
		return errors.New("certificate was replaced by a newer one")
	}
	certPEM, err := g.ca.Sign(csr, id)
	if err != nil {
		return err
	}
	cert, _ := pem.Decode(certPEM)
	parsed, err := x509.ParseCertificate(cert.Bytes)
	if err != nil {
		return err
	}
	if n, err := g.q.SetServerNextCert(ctx, db.SetServerNextCertParams{NextCertSerial: parsed.SerialNumber.Text(16), UpdatedAt: time.Now().UnixMilli(), ID: id, CertSerial: c.serial}); err != nil {
		return err
	} else if n != 1 {
		c.cancel()
		return errors.New("certificate was replaced by a newer one")
	}
	b, _ := proto.Encode(proto.TypeRenewed, "", proto.Renewed{CertPEM: string(certPEM)})
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, b)
}

// keepalive pings until ctx ends and cancels the connection when a ping is not
// answered in time. Pongs are read by the connection's read loop.
func keepalive(ctx context.Context, ws *websocket.Conn, every time.Duration, cancel context.CancelFunc) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pctx, done := context.WithTimeout(ctx, every)
		err := ws.Ping(pctx)
		done()
		if err != nil {
			cancel()
			return
		}
	}
}
