package gitea

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

func TestGitea(t *testing.T) {
	var got []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token gt-token" {
			http.Error(w, "no", 401)
			return
		}
		got = append(got, r.Method+" "+r.URL.Path)
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/comments") {
			json.NewEncoder(w).Encode(map[string]int64{"id": 77})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer api.Close()
	APIBase = func(string) string { return api.URL }
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, _ := vault.LoadOrCreate(t.TempDir())
	ctx := context.Background()
	if _, err := Connect(ctx, st.Q, v, "git.acme.uz", "wrong"); err == nil {
		t.Fatal("a refused token connected")
	}
	secret, err := Connect(ctx, st.Q, v, "git.acme.uz", "gt-token")
	if err != nil || len(secret) != 64 {
		t.Fatalf("%q %v", secret, err)
	}
	if !Has(ctx, st.Q, "git.acme.uz") || Has(ctx, st.Q, "gitlab.com") {
		t.Fatal("Has")
	}
	if err := (Statuses{st.Q, v}).Status(ctx, "git.acme.uz/acme/shop", "abc", "success", "yawl/production", "Live", "https://p"); err != nil {
		t.Fatal(err)
	}
	if id, err := (Comments{st.Q, v}).Comment(ctx, "git.acme.uz/acme/shop", 4, 0, "preview"); err != nil || id != 77 {
		t.Fatalf("%d %v", id, err)
	}
	(Comments{st.Q, v}).Comment(ctx, "git.acme.uz/acme/shop", 4, 77, "preview again")
	want := []string{"GET /api/v1/user", "POST /api/v1/repos/acme/shop/statuses/abc", "POST /api/v1/repos/acme/shop/issues/4/comments", "PATCH /api/v1/repos/acme/shop/issues/comments/77"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", got)
	}
	if tok, _ := (Tokens{st.Q, v}).Token(ctx, "https://git.acme.uz/acme/shop"); tok != "gt-token" {
		t.Fatalf("%q", tok)
	}
	hosts, _ := Load(ctx, st.Q, v)
	body := []byte(`{"ref":"refs/heads/main"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if _, ok := Verify(hosts, "git.acme.uz", body, hex.EncodeToString(mac.Sum(nil))); !ok {
		t.Fatal("a right signature was refused")
	}
	if _, ok := Verify(hosts, "git.acme.uz", body, strings.Repeat("0", 64)); ok {
		t.Fatal("a wrong signature passed")
	}
	if _, ok := Verify(hosts, "evil.example", body, hex.EncodeToString(mac.Sum(nil))); ok {
		t.Fatal("another host passed")
	}
}
