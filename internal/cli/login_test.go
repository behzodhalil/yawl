package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLoginPrintsURL(t *testing.T) {
	var code string
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/cli-login":
			var in struct{ Code, Host string }
			json.NewDecoder(r.Body).Decode(&in)
			code = in.Code
			w.WriteHeader(201)
		case r.Method == "GET" && r.URL.Path == "/api/cli-login/"+code:
			if gets++; r.URL.Query().Get("check") != "ABCD2345" {
				t.Errorf("polled with %q", r.URL.RawQuery)
			}
			io.WriteString(w, `{"token":"yawl_FROMBROWSER"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "c.json")
	t.Setenv("YAWL_CONFIG", cfg)
	out := &bytes.Buffer{}
	SetIO(out, strings.NewReader("\nABCD-2345\n abcd2345 \n"))
	if err := Run(context.Background(), "login", []string{srv.URL}); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`Open `+regexp.QuoteMeta(srv.URL)+`/cli-login#[A-Z2-7]{10} and approve this computer\.`).MatchString(out.String()) || gets != 1 {
		t.Fatalf("%d polls: %q", gets, out.String())
	}
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), "yawl_FROMBROWSER") {
		t.Fatalf("%s", b)
	}
}

func TestLoginFails(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		stdin  io.Reader
		want   string
	}{
		{404, "The login expired; run yawl login again.", strings.NewReader("ABCD2345\n"), "The login expired; run yawl login again."},
		{403, "That is not the code the browser showed. Run yawl login again.", strings.NewReader("ABCD2345\n"), "That is not the code the browser showed. Run yawl login again."},
		{200, `{"token":"yawl_X"}`, &eofs{t: t}, "No code typed; run yawl login again."},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				w.WriteHeader(201)
				return
			}
			w.WriteHeader(c.status)
			io.WriteString(w, c.body)
		}))
		t.Setenv("YAWL_CONFIG", filepath.Join(t.TempDir(), "c.json"))
		SetIO(&bytes.Buffer{}, c.stdin)
		if err := Run(context.Background(), "login", []string{srv.URL}); err == nil || err.Error() != c.want {
			t.Errorf("%d: %v", c.status, err)
		}
		srv.Close()
	}
}

type eofs struct {
	t *testing.T
	n int
}

func (e *eofs) Read([]byte) (int, error) {
	if e.n++; e.n > 10 {
		e.t.Fatal("read past EOF in a loop")
	}
	return 0, io.EOF
}

func TestLoginRefusedShowsWhy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Too many sign-in codes are waiting from this address. Wait 10 minutes or use --token.", 429)
	}))
	defer srv.Close()
	t.Setenv("YAWL_CONFIG", filepath.Join(t.TempDir(), "c.json"))
	SetIO(&bytes.Buffer{}, &eofs{t: t})
	err := Run(context.Background(), "login", []string{srv.URL})
	if err == nil || err.Error() != "the panel refused the login (429 Too Many Requests): Too many sign-in codes are waiting from this address. Wait 10 minutes or use --token." {
		t.Fatalf("%v", err)
	}
}
