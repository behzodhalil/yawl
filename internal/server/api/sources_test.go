package api

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPISources(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	box := base64.StdEncoding.EncodeToString([]byte("sealed"))
	P := "/api/environments/acme/production/sources/"
	if res, b := call(t, srv, tok, "PUT", P+"vault", `{"url":"https://vault.acme","path":"secret/data/shop","agent_box":"`+box+`","recovery_box":"`+box+`"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	for body, want := range map[string]string{
		P + "aws|" + `{"agent_box":"` + box + `","recovery_box":"` + box + `"}`:                                                                "doppler, infisical or vault",
		P + "vault|" + `{"url":"https://v","agent_box":"` + box + `","recovery_box":"` + box + `"}`:                                            "path",
		P + "doppler|" + `{"token":"dp.st.plain"}`:                                                                                             "sealed",
		P + "vault|" + `{"url":"http://vault.acme","path":"p","agent_box":"` + box + `","recovery_box":"` + box + `"}`:                         "https",
		P + "vault|" + `{"url":"https://169.254.169.254","path":"p","agent_box":"` + box + `","recovery_box":"` + box + `"}`:                   "https",
		P + "infisical|" + `{"url":"https://127.0.0.1:8080","workspace":"w","env":"e","agent_box":"` + box + `","recovery_box":"` + box + `"}`: "https",
		P + "vault|" + `{"url":"https://localhost","path":"p","agent_box":"` + box + `","recovery_box":"` + box + `"}`:                         "https",
	} {
		path, b, _ := strings.Cut(body, "|")
		if res, out := call(t, srv, tok, "PUT", path, b); res.StatusCode != 400 || !strings.Contains(string(out), want) {
			t.Errorf("%s: %d %s", path, res.StatusCode, out)
		}
	}
	_, b := call(t, srv, tok, "GET", "/api/environments/acme/production/sources", "")
	if !strings.Contains(string(b), `"provider":"vault"`) || strings.Contains(string(b), box) {
		t.Fatalf("%s", b)
	}
	if res, _ := call(t, srv, tok, "DELETE", P+"vault", ""); res.StatusCode != 204 {
		t.Fatalf("%d", res.StatusCode)
	}
}

func TestManagerURLPrivateRanges(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://10.0.0.1":        true,
		"https://10.73.0.2":       true,
		"https://172.16.0.1":      true,
		"https://192.168.1.1":     true,
		"https://[fd00::1]":       true,
		"https://127.0.0.1":       false,
		"https://[::1]":           false,
		"https://169.254.169.254": false,
		"https://0.0.0.0":         false,
		"http://10.0.0.1":         false,
		"https://u:p@10.0.0.1":    false,
	} {
		if got := managerURLNotLocal(t.Context(), raw); got != want {
			t.Errorf("managerURLNotLocal(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestManagedTakesPrivateSourceURL(t *testing.T) {
	_, tok, a := setupApp(t)
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "https://acme.cloud.test", Managed: true, Version: "0.1.0"}))
	defer srv.Close()
	call(t, srv, tok, "POST", "/api/apply", yaml)
	box := base64.StdEncoding.EncodeToString([]byte("sealed"))
	if res, b := call(t, srv, tok, "PUT", "/api/environments/acme/production/sources/vault", `{"url":"https://10.73.0.2:8200","path":"secret/data/x","agent_box":"`+box+`","recovery_box":"`+box+`"}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}
