package conn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/behzodhalil/yawl/internal/agent/state"
	"github.com/behzodhalil/yawl/internal/proto"
)

type WaitUntilNoCommandInFlight func(context.Context) error

type Handler func(ctx context.Context, cmd proto.Command) proto.Result

type Config struct {
	Dir                                        string
	ServerURL, ServerID, CertPEM, AgentVersion string
	Key                                        *ecdsa.PrivateKey
	HeartbeatEvery                             time.Duration
	RenewBefore                                time.Duration
	Stats                                      func() proto.Heartbeat
	Follow                                     func(ctx context.Context, serverVersion string, waitIdle WaitUntilNoCommandInFlight)
	Log                                        *slog.Logger
	Terms                                      Terms
}

type Terms interface {
	Open(ctx context.Context, o proto.TermOpen, send func(typ string, body any) error)
	Input(d proto.TermData)
	Resize(r proto.TermResize)
	Close(id string)
}

func LoadConfig(dir, agentVersion string) (Config, error) {
	read := func(n string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, n))
		return strings.TrimSpace(string(b)), err
	}
	var c Config
	var err error
	if c.ServerURL, err = read("server_url"); err != nil {
		return c, err
	}
	if c.ServerID, err = read("server_id"); err != nil {
		return c, err
	}
	if c.CertPEM, err = read("agent.crt"); err != nil {
		return c, err
	}
	keyPEM, err := read("agent.key")
	if err != nil {
		return c, err
	}
	blk, _ := pem.Decode([]byte(keyPEM))
	if blk == nil {
		return c, errors.New("agent.key: not PEM")
	}
	if c.Key, err = x509.ParseECPrivateKey(blk.Bytes); err != nil {
		return c, err
	}
	c.Dir, c.AgentVersion = dir, agentVersion
	c.HeartbeatEvery = 10 * time.Second
	c.RenewBefore = renewBefore
	return c, nil
}

func Dial(ctx context.Context, cfg Config, st *state.State, h Handler) error {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	r := &runner{cfg: cfg, st: st, h: h, inflight: map[string]chan struct{}{}}
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := r.session(ctx)
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, errRenewed) {
			continue
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		cfg.Log.Warn("connection to server lost", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return ctx.Err()
}

type runner struct {
	cfg       Config
	st        *state.State
	h         Handler
	mu        sync.Mutex
	inflight  map[string]chan struct{}
	newKey    *ecdsa.PrivateKey
	lastRenew time.Time
}

var errRenewed = errors.New("certificate renewed")

func wsURL(u string) string {
	u = strings.Replace(u, "https://", "wss://", 1)
	return strings.Replace(u, "http://", "ws://", 1) + "/agent"
}

func (r *runner) session(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(r.cfg.ServerURL), nil)
	if err != nil {
		return err
	}
	defer c.CloseNow()
	c.SetReadLimit(4 << 20)

	if err := r.handshake(ctx, c); err != nil {
		return err
	}
	var wmu sync.Mutex
	send := func(typ string, body any) error {
		b, err := proto.Encode(typ, "", body)
		if err != nil {
			return err
		}
		wmu.Lock()
		defer wmu.Unlock()
		return c.Write(ctx, websocket.MessageText, b)
	}
	go func() {
		t := time.NewTicker(r.cfg.HeartbeatEvery)
		defer t.Stop()
		for {
			if err := r.maybeRenew(send); err != nil {
				r.cfg.Log.Warn("certificate renewal", "err", err)
			}
			hb := proto.Heartbeat{}
			if r.cfg.Stats != nil {
				hb = r.cfg.Stats()
			}
			if send(proto.TypeHeartbeat, hb) != nil {
				cancel()
				return
			}
			// A write into a dead socket succeeds; only an answered ping proves the line is up.
			pctx, done := context.WithTimeout(ctx, max(r.cfg.HeartbeatEvery, 10*time.Second))
			err := c.Ping(pctx)
			done()
			if err != nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return err
		}
		env, err := proto.Decode(b)
		if err != nil {
			return err
		}
		if env.Type == proto.TypeRenewed {
			if err := r.saveRenewed(env.Body); err != nil {
				return err
			}
			return errRenewed
		}
		if r.term(ctx, env, send) {
			continue
		}
		if env.Type != proto.TypeCommand {
			continue
		}
		var cmd proto.Command
		if err := json.Unmarshal(env.Body, &cmd); err != nil {
			continue
		}
		go func() {
			res := r.run(context.WithoutCancel(ctx), cmd)
			send(proto.TypeResult, res)
		}()
	}
}

func (r *runner) handshake(ctx context.Context, c *websocket.Conn) error {
	_, b, err := c.Read(ctx)
	if err != nil {
		return err
	}
	ch, err := readChallenge(b)
	if r.cfg.Follow != nil && ch.ServerVersion != "" {
		r.cfg.Follow(ctx, ch.ServerVersion, r.idle)
	}
	if err != nil {
		return err
	}
	sum := sha256.Sum256(proto.AuthMessage(ch.Nonce))
	sig, err := ecdsa.SignASN1(rand.Reader, r.cfg.Key, sum[:])
	if err != nil {
		return err
	}
	hello, err := proto.Encode(proto.TypeHello, "", proto.Hello{
		AgentVersion: r.cfg.AgentVersion, Protocol: proto.Version, ServerID: r.cfg.ServerID, CertPEM: r.cfg.CertPEM, Sig: sig,
	})
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, hello)
}

func readChallenge(b []byte) (proto.Challenge, error) {
	var env proto.Envelope
	var ch proto.Challenge
	if err := json.Unmarshal(b, &env); err != nil || env.Type != proto.TypeChallenge || json.Unmarshal(env.Body, &ch) != nil {
		return ch, errors.New("server sent no challenge")
	}
	if env.Version != proto.Version {
		return ch, fmt.Errorf("%w: got %d, want %d", proto.ErrVersion, env.Version, proto.Version)
	}
	if len(ch.Nonce) < 16 {
		return ch, errors.New("server sent no challenge")
	}
	return ch, nil
}

func (r *runner) idle(ctx context.Context) error {
	for {
		r.mu.Lock()
		n := len(r.inflight)
		r.mu.Unlock()
		if n == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (r *runner) run(ctx context.Context, cmd proto.Command) proto.Result {
	if proto.Unrecorded(cmd.Name) {
		res := r.h(ctx, cmd)
		res.Key = cmd.Key
		return res
	}
	for {
		if res, ok, err := r.st.Result(cmd.Key); err != nil {
			return proto.Result{Key: cmd.Key, Cause: fmt.Sprintf("agent state: %v", err)}
		} else if ok {
			return res
		}
		r.mu.Lock()
		wait, busy := r.inflight[cmd.Key]
		if !busy {
			done := make(chan struct{})
			r.inflight[cmd.Key] = done
			r.mu.Unlock()
			res := r.h(ctx, cmd)
			res.Key = cmd.Key
			if err := r.st.SaveResult(res); err != nil {
				r.cfg.Log.Error("save result", "key", cmd.Key, "err", err)
			}
			r.mu.Lock()
			delete(r.inflight, cmd.Key)
			r.mu.Unlock()
			close(done)
			return res
		}
		r.mu.Unlock()
		<-wait
	}
}

const renewBefore = 10 * 24 * time.Hour

func (r *runner) maybeRenew(send func(string, any) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.newKey != nil {
		return nil
	}
	blk, _ := pem.Decode([]byte(r.cfg.CertPEM))
	if blk == nil {
		return errors.New("agent.crt is not PEM")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return err
	}
	if cert.NotAfter.Sub(time.Now()) > r.cfg.RenewBefore || time.Since(r.lastRenew) < time.Hour {
		return nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: r.cfg.ServerID}}, key)
	if err != nil {
		return err
	}
	r.newKey = key
	return send(proto.TypeRenew, proto.Renew{CSRPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))})
}

func (r *runner) saveRenewed(body json.RawMessage) error {
	var rn proto.Renewed
	if err := json.Unmarshal(body, &rn); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.newKey == nil {
		return errors.New("renewed certificate without a request")
	}
	kder, err := x509.MarshalECPrivateKey(r.newKey)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(r.cfg.Dir, "agent.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(r.cfg.Dir, "agent.crt"), []byte(rn.CertPEM)); err != nil {
		return err
	}
	r.cfg.Key, r.cfg.CertPEM, r.newKey, r.lastRenew = r.newKey, rn.CertPEM, nil, time.Now()
	return nil
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (r *runner) term(ctx context.Context, env proto.Envelope, send func(string, any) error) bool {
	switch env.Type {
	case proto.TypeTermOpen:
		var o proto.TermOpen
		if json.Unmarshal(env.Body, &o) == nil {
			if r.cfg.Terms == nil {
				send(proto.TypeTermEnd, proto.TermEnd{ID: o.ID, Cause: "this agent has no terminals"})
			} else {
				r.cfg.Terms.Open(ctx, o, send)
			}
		}
	case proto.TypeTermIn:
		var d proto.TermData
		if json.Unmarshal(env.Body, &d) == nil && r.cfg.Terms != nil {
			r.cfg.Terms.Input(d)
		}
	case proto.TypeTermResize:
		var z proto.TermResize
		if json.Unmarshal(env.Body, &z) == nil && r.cfg.Terms != nil {
			r.cfg.Terms.Resize(z)
		}
	case proto.TypeTermClose:
		var e proto.TermEnd
		if json.Unmarshal(env.Body, &e) == nil && r.cfg.Terms != nil {
			r.cfg.Terms.Close(e.ID)
		}
	default:
		return false
	}
	return true
}
