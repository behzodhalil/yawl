package integration

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/conn"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type pauseAfterPromote struct {
	db.DBTX
	once         sync.Once
	hit, release chan struct{}
	releaseOnce  sync.Once
}

func (p *pauseAfterPromote) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	res, err := p.DBTX.ExecContext(ctx, q, args...)
	if strings.Contains(q, "cert_serial = next_cert_serial") {
		p.once.Do(func() { close(p.hit); <-p.release })
	}
	return res, err
}

func (p *pauseAfterPromote) let() { p.releaseOnce.Do(func() { close(p.release) }) }

func TestRejoinDropsHelloAuthenticatingDuringIt(t *testing.T) {
	p := &pauseAfterPromote{hit: make(chan struct{}), release: make(chan struct{})}
	r := newRigOn(t, func(d db.DBTX) db.DBTX { p.DBTX = d; return p })
	t.Cleanup(p.let)

	old, _ := conn.LoadConfig(r.agentDir, "test")
	c := rawHello(t, r, old.Key, old.CertPEM)
	waitFor(t, "the old agent never connected", func() bool { return r.gw.Connected(r.serverID) })
	c2, k2 := rawRenew(t, r, c)
	if c2 == "" {
		t.Fatal("no renewal")
	}
	c.CloseNow()
	waitFor(t, "the old agent never left", func() bool { return !r.gw.Connected(r.serverID) })

	stale := rawHello(t, r, k2, c2)
	select {
	case <-p.hit:
	case <-time.After(5 * time.Second):
		t.Fatal("the hello never reached the promote")
	}
	rejoin(t, r)
	p.let()
	if !refused(stale) {
		t.Fatal("a hello that authenticated during the re-join outlived it")
	}
}

type rejoinBeforeNext struct {
	db.DBTX
	once sync.Once
}

func (p *rejoinBeforeNext) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, "SET next_cert_serial = ?") {
		p.once.Do(func() {
			db.New(p.DBTX).SetServerCert(ctx, db.SetServerCertParams{CertSerial: "re-joined", UpdatedAt: time.Now().UnixMilli(), ID: args[2].(string)})
		})
	}
	return p.DBTX.ExecContext(ctx, q, args...)
}

func TestRenewLosesToRejoinBetweenCheckAndWrite(t *testing.T) {
	r := newRigOn(t, func(d db.DBTX) db.DBTX { return &rejoinBeforeNext{DBTX: d} })
	old, _ := conn.LoadConfig(r.agentDir, "test")
	c := rawHello(t, r, old.Key, old.CertPEM)
	waitFor(t, "the old agent never connected", func() bool { return r.gw.Connected(r.serverID) })
	if got, _ := rawRenew(t, r, c); got != "" {
		t.Fatal("the old connection got a certificate after the re-join")
	}
	srv, err := r.st.Q.GetServer(context.Background(), r.serverID)
	if err != nil || srv.CertSerial != "re-joined" || srv.NextCertSerial != "" {
		t.Fatalf("%v cert %q next %q", err, srv.CertSerial, srv.NextCertSerial)
	}
}
