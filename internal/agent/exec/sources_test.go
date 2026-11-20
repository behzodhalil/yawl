package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestMain(m *testing.M) {
	loopbackOK = true
	os.Exit(m.Run())
}

func noLoopback(t *testing.T) {
	loopbackOK = false
	t.Cleanup(func() { loopbackOK = true })
}

func TestSecretSources(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/configs/config/secrets/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dp.st.token" || r.URL.Query().Get("format") != "json" {
			http.Error(w, "nope", 401)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"STRIPE_KEY": "sk_doppler", "SHARED": "from-doppler"})
	})
	mux.HandleFunc("/v1/secret/data/shop", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "hvs.token" {
			http.Error(w, "permission denied", 403)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": map[string]any{"MAIL_PASS": "from-vault"}}})
	})
	mux.HandleFunc("/api/v1/auth/universal-auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ClientID, ClientSecret string }
		json.NewDecoder(r.Body).Decode(&in)
		if in.ClientID != "cid" || in.ClientSecret != "csecret" {
			http.Error(w, "bad", 401)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"accessToken": "inf-access"})
	})
	mux.HandleFunc("/api/v3/secrets/raw", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Header.Get("Authorization") != "Bearer inf-access" || q.Get("workspaceId") != "ws1" || q.Get("environment") != "prod" || q.Get("secretPath") != "/" {
			http.Error(w, "bad", 403)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"secrets": []map[string]string{{"secretKey": "S3_KEY", "secretValue": "from-infisical"}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	kr, seal := keyring(t)
	d.Keys = kr
	d.DopplerURL = srv.URL
	vars := []proto.SealedVar{
		{Key: "doppler", Box: seal("dp.st.token"), Source: &proto.SecretSource{Provider: "doppler"}},
		{Key: "vault", Box: seal("hvs.token"), Source: &proto.SecretSource{Provider: "vault", URL: srv.URL, Path: "secret/data/shop"}},
		{Key: "infisical", Box: seal("cid:csecret"), Source: &proto.SecretSource{Provider: "infisical", URL: srv.URL, Workspace: "ws1", Env: "prod"}},
		{Key: "SHARED", Box: seal("from-yawl")},
	}
	env, cause := d.env(vars)
	if cause != "" {
		t.Fatal(cause)
	}
	for _, want := range []string{"STRIPE_KEY=sk_doppler", "MAIL_PASS=from-vault", "S3_KEY=from-infisical", "SHARED=from-yawl"} {
		if !slices.Contains(env, want) {
			t.Errorf("no %s in %v", want, env)
		}
	}
	if slices.Contains(env, "SHARED=from-doppler") {
		t.Errorf("the provider's value was kept over Yawl's: %v", env)
	}
	if got := kr.Masker().Mask("sk_doppler from-vault from-infisical csecret inf-access"); strings.Count(got, "••••") != 5 {
		t.Errorf("not masked: %q", got)
	}
	_, cause = d.env([]proto.SealedVar{{Key: "vault", Box: seal("wrong-token"), Source: &proto.SecretSource{Provider: "vault", URL: srv.URL, Path: "secret/data/shop"}}})
	if !strings.Contains(cause, "vault") || !strings.Contains(cause, "403") || strings.Contains(cause, "wrong-token") {
		t.Fatalf("%q", cause)
	}

	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/v1/secret/data/shop", http.StatusTemporaryRedirect)
	}))
	defer away.Close()
	if _, cause = d.env([]proto.SealedVar{{Key: "vault", Box: seal("hvs.token"), Source: &proto.SecretSource{Provider: "vault", URL: away.URL, Path: "x"}}}); !strings.Contains(cause, "307") {
		t.Fatalf("a redirect was followed: %q", cause)
	}
}

func TestVaultValues(t *testing.T) {
	body := `{"data":{"data":{"PORT":1000000,"RATIO":0.5,"ON":true,"CFG":{"a":1},"NAME":"shop"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	kr, seal := keyring(t)
	d.Keys = kr
	v := []proto.SealedVar{{Key: "vault", Box: seal("t"), Source: &proto.SecretSource{Provider: "vault", URL: srv.URL, Path: "secret/data/shop"}}}
	env, cause := d.env(v)
	for _, want := range []string{"PORT=1000000", "RATIO=0.5", "ON=true", `CFG={"a":1}`, "NAME=shop"} {
		if cause != "" || !slices.Contains(env, want) {
			t.Errorf("no %s in %v %q", want, env, cause)
		}
	}
	body = `{"data":{"data":{"A=B":"x"}}}`
	if _, cause = d.env(v); !strings.Contains(cause, `"A=B"`) {
		t.Fatalf("a key with = taken: %q", cause)
	}
}

func TestSecretSourceOracle(t *testing.T) {
	var gotToken string
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Vault-Token")
		if r.URL.Path == "/v1/ok" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": map[string]any{"INTERNAL": "leaked-as-env"}}})
			return
		}
		http.Error(w, "internal-service-banner "+strings.Repeat("x", 400), 403)
	}))
	defer internal.Close()
	hit := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer elsewhere.Close()
	redirect := httptest.NewServer(http.RedirectHandler(elsewhere.URL+"/v1/ok", http.StatusFound))
	defer redirect.Close()
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})

	noLoopback(t)
	for _, u := range []string{internal.URL, "http://169.254.169.254", "http://[fe80::1]:8200"} {
		if _, err := d.fetchFrom(proto.SecretSource{Provider: "vault", URL: u, Path: "ok"}, "hvs.sealed"); err == nil || !strings.Contains(err.Error(), "Yawl does not fetch from it") || gotToken != "" {
			t.Fatalf("%s dialed: %v (token %q)", u, err, gotToken)
		}
	}
	loopbackOK = true

	_, err := d.fetchFrom(proto.SecretSource{Provider: "vault", URL: internal.URL, Path: "denied"}, "hvs.sealed")
	if err == nil || !strings.Contains(err.Error(), "internal-service-banner") || gotToken != "hvs.sealed" {
		t.Fatalf("refusal body not quoted or token not sent: %v (token %q)", err, gotToken)
	}
	if quoted := err.Error()[strings.Index(err.Error(), "internal-service-banner"):]; len(quoted) > 300 {
		t.Fatalf("quoted %d bytes, the cap is 300", len(quoted))
	}
	out, err := d.fetchFrom(proto.SecretSource{Provider: "vault", URL: internal.URL, Path: "ok"}, "hvs.sealed")
	if err != nil || out["INTERNAL"] != "leaked-as-env" {
		t.Fatalf("200 not turned into variables: %v %v", out, err)
	}
	if _, err := d.fetchFrom(proto.SecretSource{Provider: "vault", URL: redirect.URL, Path: "x"}, "hvs.sealed"); err == nil || hit {
		t.Fatalf("redirect followed with the token: err=%v hit=%v", err, hit)
	}
}

func TestGuardRefusesCloudMetadata(t *testing.T) {
	noLoopback(t)
	for addr, refused := range map[string]bool{
		"169.254.169.254:80":  true,
		"[fe80::1]:80":        true,
		"[fd00:ec2::254]:80":  true,
		"127.0.0.1:8200":      true,
		"0.0.0.0:80":          true,
		"10.73.0.2:8200":      false,
		"[fd00:ec2::253]:443": false,
		"[fd00::1]:443":       false,
	} {
		if err := guarded.Control("tcp", addr, nil); (err != nil) != refused {
			t.Errorf("%s: %v", addr, err)
		}
	}
}
