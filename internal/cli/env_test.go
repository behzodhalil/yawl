package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agekey"
	"github.com/behzodhalil/yawl/internal/seal"
)

type envAPI struct {
	agentPub, recPub   seal.PublicKey
	agentPriv, recPriv seal.PrivateKey
	puts               map[string]map[string]any
	recoverySet        string
	backupKeySet       string
}

func newEnvAPI(t *testing.T) (*envAPI, *httptest.Server) {
	a := &envAPI{puts: map[string]map[string]any{}}
	a.agentPub, a.agentPriv, _ = seal.GenerateKey()
	a.recPub, a.recPriv, _ = seal.GenerateKey()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/api/recovery-key" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"set": true, "pubkey": a.recPub.String()})
		case p == "/api/recovery-key":
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			a.recoverySet, a.backupKeySet = in["pubkey"], in["backup_key"]
		case strings.HasSuffix(p, "/keys"):
			json.NewEncoder(w).Encode(map[string]string{"agent_pubkey": a.agentPub.String(), "recovery_pubkey": a.recPub.String(), "server": "hel1"})
		case strings.HasSuffix(p, "/reveal"):
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			to, _ := seal.ParsePublicKey(in["to"])
			box, _ := seal.Seal(to, []byte("from-the-agent"))
			json.NewEncoder(w).Encode(map[string]string{"box": box})
		case strings.HasSuffix(p, "/recovery_box"):
			box, _ := seal.Seal(a.recPub, []byte("from-recovery"))
			json.NewEncoder(w).Encode(map[string]string{"box": box})
		case p == "/api/environments/acme/production/env":
			io.WriteString(w, `[{"key":"DB","scope":"shared"},{"key":"SECRET","scope":"service","service":"api"}]`)
		case p == "/api/services/acme/production/api/env":
			io.WriteString(w, `[{"key":"NPM_TOKEN","scope":"service","build":true},{"key":"DB","scope":"shared"},{"key":"DATABASE_URL","scope":"service","ref":"acme-db.url"}]`)
		case p == "/api/environments/acme/staging/env":
			io.WriteString(w, `[{"key":"DB","scope":"shared"},{"key":"DEBUG","scope":"shared"}]`)
		case r.Method == "PUT":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			a.puts[p] = body
			io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return a, srv
}

func envSetup(t *testing.T) (*envAPI, *bytes.Buffer) {
	a, srv := newEnvAPI(t)
	dir := t.TempDir()
	t.Setenv("YAWL_CONFIG", filepath.Join(dir, "config.json"))
	out := &bytes.Buffer{}
	stdout, stdin = out, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	out.Reset()
	os.WriteFile(filepath.Join(dir, "yawl.yaml"), []byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"), 0o644)
	t.Chdir(dir)
	return a, out
}

func TestEnvSetSealsLocally(t *testing.T) {
	a, out := envSetup(t)
	stdin = strings.NewReader("multi\nline=value\n")
	if err := Run(context.Background(), "env", []string{"set", "api", "SECRET"}); err != nil {
		t.Fatal(err)
	}
	body := a.puts["/api/services/acme/production/api/env/SECRET"]
	if _, plain := body["value"]; plain || body == nil {
		t.Fatalf("body %v", body)
	}
	for _, k := range []struct {
		box  string
		pub  seal.PublicKey
		priv seal.PrivateKey
	}{{body["agent_box"].(string), a.agentPub, a.agentPriv}, {body["recovery_box"].(string), a.recPub, a.recPriv}} {
		got, err := seal.Open(k.pub, k.priv, k.box)
		if err != nil || string(got) != "multi\nline=value" {
			t.Fatalf("opened %q (%v)", got, err)
		}
	}
	if !strings.Contains(out.String(), "Takes effect on the next deploy: yawl deploy api") {
		t.Fatalf("out:\n%s", out)
	}
}

func TestEnvSetShared(t *testing.T) {
	a, _ := envSetup(t)
	stdin = strings.NewReader("v")
	Run(context.Background(), "env", []string{"set", "api", "DB", "--shared"})
	if a.puts["/api/environments/acme/production/env/DB"] == nil {
		t.Fatalf("puts %v", a.puts)
	}
}

func TestEnvRevealViaAgent(t *testing.T) {
	_, out := envSetup(t)
	if err := Run(context.Background(), "env", []string{"reveal", "api", "SECRET"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "from-the-agent" {
		t.Fatalf("%q", out)
	}
}

func TestEnvRevealRecovery(t *testing.T) {
	a, out := envSetup(t)
	stdin = strings.NewReader(seal.EncodeRecovery(a.recPriv) + "\n")
	if err := Run(context.Background(), "env", []string{"reveal", "api", "SECRET", "--recovery"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out.String()), "from-recovery") {
		t.Fatalf("%q", out)
	}
}

func TestEnvDiff(t *testing.T) {
	_, out := envSetup(t)
	if err := Run(context.Background(), "env", []string{"diff", "staging", "production"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"only in staging: DEBUG", "only in production: api/SECRET"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRecoveryKeyInit(t *testing.T) {
	a, out := envSetup(t)
	if err := Run(context.Background(), "recovery-key", []string{"init"}); err != nil {
		t.Fatal(err)
	}
	line := strings.Fields(out.String())
	var key string
	for _, f := range line {
		if strings.HasPrefix(f, "yawl-recovery-") {
			key = f
		}
	}
	pub, _, err := seal.DecodeRecovery(key)
	if err != nil || pub.String() != a.recoverySet || !strings.Contains(out.String(), "Yawl cannot show it again") {
		t.Fatalf("printed %q, sent %q (%v)", key, a.recoverySet, err)
	}
	if want, _ := agekey.BackupSigningKey(key); a.backupKeySet != base64.StdEncoding.EncodeToString(want) {
		t.Fatalf("backup key %q", a.backupKeySet)
	}
}

func TestSignBackups(t *testing.T) {
	a, out := envSetup(t)
	_, other, _ := seal.GenerateKey()
	stdin = strings.NewReader(seal.EncodeRecovery(other) + "\n")
	if err := Run(context.Background(), "recovery-key", []string{"sign-backups"}); err == nil || err.Error() != "That is not this panel's recovery key." {
		t.Fatalf("another key: %v", err)
	}
	if a.puts["/api/recovery-key/backup-key"] != nil {
		t.Fatal("sent a key from another recovery key")
	}
	text := seal.EncodeRecovery(a.recPriv)
	stdin = strings.NewReader(text + "\n")
	if err := Run(context.Background(), "recovery-key", []string{"sign-backups"}); err != nil {
		t.Fatal(err)
	}
	want, _ := agekey.BackupSigningKey(text)
	if a.puts["/api/recovery-key/backup-key"]["key"] != base64.StdEncoding.EncodeToString(want) || strings.Contains(out.String(), text) {
		t.Fatalf("sent %v, printed %q", a.puts["/api/recovery-key/backup-key"], out)
	}
}

func TestReadValueExactAndLimited(t *testing.T) {
	for in, want := range map[string]string{"a\r": "a\r", "x\r\n": "x", "y\n": "y", "two\nlines\n": "two\nlines", "z": "z"} {
		stdin = strings.NewReader(in)
		if got, err := readValue("K"); err != nil || got != want {
			t.Fatalf("%q -> %q (%v), want %q", in, got, err, want)
		}
	}
	stdin = strings.NewReader(strings.Repeat("a", seal.MaxValue) + "\n")
	if _, err := readValue("K"); err != nil {
		t.Fatalf("exactly the limit plus a newline: %v", err)
	}
	stdin = strings.NewReader(strings.Repeat("a", seal.MaxValue+1) + "\n")
	if _, err := readValue("K"); err == nil {
		t.Fatal("a value over the limit was accepted")
	}
}

func TestEnvSetBuildFlag(t *testing.T) {
	a, out := envSetup(t)
	stdin = strings.NewReader("tok")
	if err := Run(context.Background(), "env", []string{"set", "--build", "api", "NPM_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	if body := a.puts["/api/services/acme/production/api/env/NPM_TOKEN"]; body == nil || body["build"] != true {
		t.Fatalf("body %v", body)
	}
	stdin = strings.NewReader("v")
	Run(context.Background(), "env", []string{"set", "api", "PLAIN"})
	if body := a.puts["/api/services/acme/production/api/env/PLAIN"]; body["build"] == true {
		t.Fatalf("PLAIN sent as build: %v", body)
	}
	out.Reset()
	if err := Run(context.Background(), "env", []string{"ls", "api"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "NPM_TOKEN") || !strings.Contains(strings.Split(out.String(), "\n")[0], "build") ||
		strings.Contains(strings.Split(out.String(), "\n")[1], "build") {
		t.Fatalf("ls:\n%s", out)
	}
}

func TestEnvSetRef(t *testing.T) {
	a, out := envSetup(t)
	if err := Run(context.Background(), "env", []string{"set", "api", "DATABASE_URL", "--ref", "acme-db.url"}); err != nil {
		t.Fatal(err)
	}
	body := a.puts["/api/services/acme/production/api/env/DATABASE_URL"]
	if body["ref"] != "acme-db.url" || body["agent_box"] != nil || body["recovery_box"] != nil {
		t.Fatalf("body %v", body)
	}
	if !strings.Contains(out.String(), "DATABASE_URL now refers to acme-db.url.") {
		t.Fatalf("out %q", out)
	}
	out.Reset()
	Run(context.Background(), "env", []string{"ls", "api"})
	if !strings.Contains(out.String(), "→ acme-db.url") {
		t.Fatalf("ls %q", out)
	}
	out.Reset()
	err := Run(context.Background(), "env", []string{"reveal", "api", "DATABASE_URL"})
	if err == nil || err.Error() != "DATABASE_URL is a reference to acme-db.url; reveal the database's URL with: yawl env reveal acme-db URL" {
		t.Fatalf("reveal err %v", err)
	}
}
