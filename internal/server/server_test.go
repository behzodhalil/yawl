package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
)

func TestTelegramTokenSealed(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	args := []string{"--data", filepath.Join(dir, "data"), "--etc", filepath.Join(dir, "etc"), "telegram", "set", "123:bot-token-value", "42"}
	if err := Run(ctx, args, "dev"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "data", "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	raw, err := st.Q.GetSetting(ctx, "telegram.token")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "v1:") || strings.Contains(raw, "bot-token-value") {
		t.Fatalf("telegram.token stored as %q", raw)
	}
}

func TestStorageSecretSealed(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	args := []string{"--data", filepath.Join(dir, "data"), "--etc", filepath.Join(dir, "etc"),
		"storage", "set", "s3.eu-central-003.backblazeb2.com", "acme-backups", "AKIA-access", "very-secret-storage-key", "eu-central-003"}
	if err := Run(ctx, args, "dev"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "data", "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for key, plain := range map[string]string{"storage.endpoint": "s3.eu-central-003.backblazeb2.com", "storage.bucket": "acme-backups", "storage.region": "eu-central-003"} {
		if v, _ := st.Q.GetSetting(ctx, key); v != plain {
			t.Fatalf("%s = %q", key, v)
		}
	}
	for key, secret := range map[string]string{"storage.access_key": "AKIA-access", "storage.secret_key": "very-secret-storage-key"} {
		v, _ := st.Q.GetSetting(ctx, key)
		if !strings.HasPrefix(v, "v1:") || strings.Contains(v, secret) {
			t.Fatalf("%s stored as %q", key, v)
		}
	}
}

func TestClaimLinkExitCodes(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	data := filepath.Join(dir, "data")
	args := []string{"--data", data, "--etc", filepath.Join(dir, "etc"), "claim-link"}
	if err := Run(ctx, args, "dev"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(data, "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(st)
	tok, err := a.ClaimLink(ctx)
	if err == nil {
		_, err = a.Claim(ctx, tok, "owner@acme.uz", "correct horse battery")
	}
	st.Close()
	if err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	err = Run(ctx, args, "dev")
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if ExitCode(err) != 3 || len(out) != 0 {
		t.Fatalf("with an owner: exit %d, %v, stdout %q", ExitCode(err), err, out)
	}

	file := filepath.Join(dir, "not-a-dir")
	os.WriteFile(file, nil, 0o600)
	err = Run(ctx, []string{"--data", file, "--etc", filepath.Join(dir, "etc"), "claim-link"}, "dev")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("no database: exit %d, %v", ExitCode(err), err)
	}
}

func TestResetTwoStepCommand(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	data, etc := filepath.Join(dir, "data"), filepath.Join(dir, "etc")
	os.MkdirAll(data, 0o700)
	st, err := store.Open(filepath.Join(data, "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(st)
	tok, _ := a.ClaimLink(ctx)
	session, err := a.Claim(ctx, tok, "owner@acme.uz", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	st.DB.ExecContext(ctx, "UPDATE owner SET totp_secret = 'sealed'")
	st.Close()

	run := func(args ...string) (string, error) {
		r, w, _ := os.Pipe()
		stdout := os.Stdout
		os.Stdout = w
		err := Run(ctx, append([]string{"--data", data, "--etc", etc}, args...), "dev")
		os.Stdout = stdout
		w.Close()
		out, _ := io.ReadAll(r)
		return string(out), err
	}
	out, err := run("reset-2fa", "Owner@acme.uz")
	if err != nil || out != "Two-step sign in is off for owner@acme.uz. Their sessions have ended.\n" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := run("reset-2fa", "who@acme.uz"); err == nil || err.Error() != "No member with the email who@acme.uz." {
		t.Fatalf("unknown: %v", err)
	}
	st, _ = store.Open(filepath.Join(data, "yawl.db"))
	defer st.Close()
	a = app.New(st)
	if _, err := a.SessionOwner(ctx, session); err == nil {
		t.Fatal("the session survived")
	}
	if on, _, _, _ := a.TwoStepStatus(ctx, mustFirst(t, a)); on {
		t.Fatal("two-step is still on")
	}
	var actor, body string
	st.DB.QueryRowContext(ctx, "SELECT actor, body_json FROM events WHERE kind = 'audit' ORDER BY created_at DESC LIMIT 1").Scan(&actor, &body)
	if actor != "server command" || !strings.Contains(body, "turned off two-step sign in for owner@acme.uz") {
		t.Fatalf("audit %q %s", actor, body)
	}
}

func TestResetPasswordCommand(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	data := filepath.Join(dir, "data")
	os.MkdirAll(data, 0o700)
	st, err := store.Open(filepath.Join(data, "yawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(st)
	tok, _ := a.ClaimLink(ctx)
	if _, err := a.Claim(ctx, tok, "owner@acme.uz", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	run := func(args ...string) (string, error) {
		r, w, _ := os.Pipe()
		stdout := os.Stdout
		os.Stdout = w
		err := Run(ctx, append([]string{"--data", data, "--etc", filepath.Join(dir, "etc"), "--public-url", "https://panel.acme.uz/"}, args...), "dev")
		os.Stdout = stdout
		w.Close()
		out, _ := io.ReadAll(r)
		return string(out), err
	}
	out, err := run("reset-password", "Owner@acme.uz")
	link, ok := strings.CutPrefix(strings.TrimSpace(out), "https://panel.acme.uz/reset#")
	if err != nil || !ok || link == "" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := run("reset-password", "who@acme.uz"); err == nil || err.Error() != "No member with the email who@acme.uz." {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := run("reset-password"); err == nil || err.Error() != "usage: yawl server reset-password <email>" {
		t.Fatalf("usage: %v", err)
	}
	st, _ = store.Open(filepath.Join(data, "yawl.db"))
	defer st.Close()
	a = app.New(st)
	if _, err := a.ResetPassword(ctx, link, "a new long password"); err != nil {
		t.Fatal(err)
	}
	var actor, body string
	st.DB.QueryRowContext(ctx, "SELECT actor, body_json FROM events WHERE kind = 'audit' AND body_json LIKE '%link for a member%'").Scan(&actor, &body)
	if actor != "server command" {
		t.Fatalf("audit %q %s", actor, body)
	}
}

func mustFirst(t *testing.T, a *app.App) string {
	o, err := a.St.Q.FirstAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return o.ID
}

func TestFlagsFromEnv(t *testing.T) {
	t.Setenv("YAWL_LISTEN", "0.0.0.0:7000")
	t.Setenv("YAWL_PUBLIC_URL", "https://acme.cloud.test")
	t.Setenv("YAWL_MANAGED", "1")
	t.Setenv("YAWL_CLAIM_HASH", "ab")
	o, _, err := flags("server", []string{"--listen", "127.0.0.1:9"})
	if err != nil || o.listen != "127.0.0.1:9" || o.publicURL != "https://acme.cloud.test" || !o.managed || o.claimHash != "ab" {
		t.Fatalf("%+v %v", o, err)
	}
	if o, _, _ := flags("server", nil); o.listen != "0.0.0.0:7000" {
		t.Fatalf("%+v", o)
	}
}

func TestDefaultGateway(t *testing.T) {
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t000012AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
		"eth0\t00000000\t010012AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	if g := defaultGateway(route); g.String() != "172.18.0.1" {
		t.Fatalf("%s", g)
	}
	if g := defaultGateway("Iface\tDestination\n"); g.IsValid() {
		t.Fatalf("%s", g)
	}
}

func TestAgentPathsNotSPA(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{"--data", filepath.Join(dir, "data"), "--etc", filepath.Join(dir, "etc"), "--listen", addr}, "dev")
	}()
	t.Cleanup(func() { cancel(); <-done })
	base := "http://" + addr
	for i := 0; ; i++ {
		if res, err := http.Get(base + "/"); err == nil {
			res.Body.Close()
			break
		} else if i > 100 {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, p := range []string{"/agent/x", "/agent/join"} {
		res, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("GET %s: %d", p, res.StatusCode)
		}
	}
	res, err := http.Post(base+"/agent/join", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode == 404 || res.StatusCode == 200 || strings.Contains(string(b), "<html") {
		t.Fatalf("POST /agent/join: %d %s", res.StatusCode, b)
	}
}
