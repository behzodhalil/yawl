package cloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/behzodhalil/yawl/internal/seal"
	"github.com/behzodhalil/yawl/internal/server/api"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

type operatorWorld struct {
	st     *store.Store
	op     Operator
	mu     sync.Mutex
	paths  []string
	refuse bool
}

func newOperator(t *testing.T) *operatorWorld {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	agentPub, _, _ := seal.GenerateKey()
	recPub, _, _ := seal.GenerateKey()
	st.Q.UpsertServer(ctx, db.UpsertServerParams{ID: "srv_1", Name: "srv1", PublicIp: "198.51.100.20", AgentPubkey: agentPub.String(), CreatedAt: 1, UpdatedAt: 1})
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "recovery.pubkey", Value: recPub.String()})
	a := app.New(st)
	tok, _ := a.CreateToken(ctx, "cloud front")
	v, _ := vault.LoadOrCreate(t.TempDir())
	w := &operatorWorld{st: st}
	h := api.New(api.Deps{App: a, PublicURL: "https://panel.test", Vault: v})
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.paths = append(w.paths, r.Method+" "+r.URL.Path)
		refuse := w.refuse && r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/deploys")
		w.mu.Unlock()
		if refuse {
			http.Error(rw, "the operator is busy", 503)
			return
		}
		h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	w.op = Operator{Base: srv.URL, Token: tok, Project: "cells", Server: "srv1"}
	return w
}

func acme() Cell {
	return Cell{Name: "acme", Image: "registry.test/yawl:0.1.0", PublicURL: "https://panel-acme.198-51-100-20.sslip.io",
		ClaimHash: strings.Repeat("ab", 32), InstallURL: "https://cloud.test/install.sh", Releases: "https://cloud.test/releases",
		BillingURL: "https://cloud.test/cells/acme/billing", BillingToken: strings.Repeat("cd", 32)}
}

func TestOperatorCreate(t *testing.T) {
	w := newOperator(t)
	ctx := context.Background()
	if err := w.op.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.op.Create(ctx, acme()); err != nil {
		t.Fatal(err)
	}
	var image, health, volumes, backupAt, env string
	var port, memory int64
	w.st.DB.QueryRow(`SELECT s.image, s.port, s.health_path, s.volumes_json, s.memory, s.backup_at, e.name FROM services s JOIN environments e ON e.id = s.environment_id
		JOIN projects p ON p.id = e.project_id WHERE p.name = 'cells' AND s.name = 'panel' AND s.deleted_at IS NULL`).Scan(&image, &port, &health, &volumes, &memory, &backupAt, &env)
	if env != "acme" || image != "registry.test/yawl:0.1.0" || port != 7000 || health != "/api/session" || !strings.Contains(volumes, `"/data"`) || memory != 256e6 || backupAt != "00:55" {
		t.Fatalf("%s %s %d %s %s %d %s", env, image, port, health, volumes, memory, backupAt)
	}
	var hosts int
	w.st.DB.QueryRow(`SELECT COUNT(*) FROM domains`).Scan(&hosts)
	var keys string
	w.st.DB.QueryRow(`SELECT group_concat(key) FROM (SELECT key FROM env_vars ORDER BY key)`).Scan(&keys)
	if hosts != 0 || keys != "YAWL_BILLING_TOKEN,YAWL_BILLING_URL,YAWL_CLAIM_HASH,YAWL_INSTALL_URL,YAWL_LIMITS,YAWL_MANAGED,YAWL_PUBLIC_URL,YAWL_RELEASES" {
		t.Fatalf("%d %s", hosts, keys)
	}
	var plain int
	w.st.DB.QueryRow(`SELECT COUNT(*) FROM env_vars WHERE agent_box LIKE '%abab%' OR agent_box LIKE '%cdcd%' OR agent_box LIKE '%sslip%'`).Scan(&plain)
	if plain != 0 {
		t.Fatal("a value went unsealed")
	}
	c := acme()
	c.Name, c.Domain, c.PublicURL = "beta", "beta.yawl.cloud", "https://beta.yawl.cloud"
	if err := w.op.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	var host string
	w.st.DB.QueryRow(`SELECT host FROM domains`).Scan(&host)
	if host != "beta.yawl.cloud" {
		t.Fatalf("%q", host)
	}
	if s, err := w.op.State(ctx, "acme"); err != nil || s == "" {
		t.Fatalf("%q %v", s, err)
	}
	for _, p := range w.paths {
		if !strings.Contains(p, "/api/environments/cells/") && !strings.Contains(p, "/api/services/cells/") && p != "GET /api/projects" && p != "POST /api/apply" {
			t.Errorf("the front reached %s", p)
		}
	}
}

func TestOperatorCreateTwiceAndResume(t *testing.T) {
	w := newOperator(t)
	ctx := context.Background()
	w.refuse = true
	if err := w.op.Create(ctx, acme()); err == nil || !strings.Contains(err.Error(), "the operator is busy") {
		t.Fatalf("%v", err)
	}
	other := acme()
	other.Image = "registry.test/someone-else:9"
	if err := w.op.Create(ctx, other); err == nil || !strings.Contains(err.Error(), "acme is taken") {
		t.Fatalf("made over an existing cell: %v", err)
	}
	w.refuse = false
	again := acme()
	again.Resume = true
	if err := w.op.Create(ctx, again); err != nil {
		t.Fatalf("resume: %v", err)
	}
	var image string
	var trashed, deploys int
	w.st.DB.QueryRow(`SELECT image FROM services WHERE name = 'panel'`).Scan(&image)
	w.st.DB.QueryRow(`SELECT COUNT(*) FROM environments WHERE deleted_at IS NOT NULL`).Scan(&trashed)
	w.st.DB.QueryRow(`SELECT COUNT(*) FROM deploys`).Scan(&deploys)
	if image != "registry.test/yawl:0.1.0" || trashed != 0 || deploys != 1 {
		t.Fatalf("%s trashed %d deploys %d", image, trashed, deploys)
	}
}

func TestOperatorSetImageAndDelete(t *testing.T) {
	w := newOperator(t)
	ctx := context.Background()
	if err := w.op.Create(ctx, acme()); err != nil {
		t.Fatal(err)
	}
	if err := w.op.SetImage(ctx, "acme", "registry.test/yawl:0.2.0"); err != nil {
		t.Fatal(err)
	}
	var image string
	var deploys int
	w.st.DB.QueryRow(`SELECT image FROM services WHERE name = 'panel'`).Scan(&image)
	w.st.DB.QueryRow(`SELECT COUNT(*) FROM deploys`).Scan(&deploys)
	if image != "registry.test/yawl:0.2.0" || deploys != 2 {
		t.Fatalf("%s %d", image, deploys)
	}
	if err := w.op.Delete(ctx, "acme"); err != nil {
		t.Fatal(err)
	}
	var live int
	w.st.DB.QueryRow(`SELECT COUNT(*) FROM environments WHERE name = 'acme' AND deleted_at IS NULL`).Scan(&live)
	if live != 0 {
		t.Fatal("not deleted")
	}
}
