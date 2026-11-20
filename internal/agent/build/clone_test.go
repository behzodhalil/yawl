package build

import (
	"context"
	"fmt"
	gohttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

func TestCloneOptionsToken(t *testing.T) {
	o := cloneOptions(Request{Repo: "https://github.com/acme/api", Branch: "main", Token: "ghs_secretvalue123"})
	a, ok := o.Auth.(*http.BasicAuth)
	if !ok || a.Username != "x-access-token" || a.Password != "ghs_secretvalue123" {
		t.Fatalf("auth = %#v", o.Auth)
	}
	if strings.Contains(o.URL, "ghs_") {
		t.Fatalf("token in URL %q", o.URL)
	}
	if o := cloneOptions(Request{Repo: "https://github.com/acme/api"}); o.Auth != nil {
		t.Fatalf("auth without a token: %#v", o.Auth)
	}
}

func TestCloneErrorHidesToken(t *testing.T) {
	_, _, err := Build(context.Background(), nil, Request{Repo: "http://127.0.0.1:1/x.git", Token: "ghs_secretvalue123", Image: "yawl/x:1"})
	if err == nil {
		t.Fatal("clone of a closed port succeeded")
	}
	if strings.Contains(err.Error(), "ghs_secretvalue123") {
		t.Fatalf("error carries the token: %v", err)
	}
}

func TestCloneOptionsGitAtBecomesHTTPS(t *testing.T) {
	o := cloneOptions(Request{Repo: "git@github.com:Acme/API.git", Token: "ghs_x"})
	if o.URL != "https://github.com/acme/api.git" {
		t.Fatalf("URL %q", o.URL)
	}
	if o := cloneOptions(Request{Repo: "git@gitlab.com:a/b.git"}); o.URL != "git@gitlab.com:a/b.git" {
		t.Fatalf("non-GitHub URL changed: %q", o.URL)
	}
}

func TestSubjectFirstLine(t *testing.T) {
	if got := subject("Fix rounding\n\nThe long body."); got != "Fix rounding" {
		t.Fatalf("%q", got)
	}
	if got := subject("  \n  Second line leads  \n"); got != "Second line leads" {
		t.Fatalf("%q", got)
	}
	if got := subject(strings.Repeat("é", 300)); len([]rune(got)) != 300 {
		t.Fatalf("%d runes", len([]rune(got)))
	}
}

func TestCloneReportsSubject(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	w, _ := repo.Worktree()
	w.Add("a.txt")
	sig := &object.Signature{Name: "t", Email: "t@x", When: time.Unix(1, 0)}
	sha, err := w.Commit("Add PDF export\n\nbody", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatal(err)
	}
	out, got, err := clone(context.Background(), Request{Repo: "file://" + dir})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(out)
	if got.Commit != sha.String() || got.Subject != "Add PDF export" {
		t.Fatalf("%+v", got)
	}
}

func TestCloneAuthRequiredSaysConnect(t *testing.T) {
	srv := httptest.NewServer(gohttp.HandlerFunc(func(w gohttp.ResponseWriter, _ *gohttp.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
		w.WriteHeader(gohttp.StatusUnauthorized)
	}))
	defer srv.Close()
	_, _, err := clone(context.Background(), Request{Repo: srv.URL + "/acme/api.git"})
	if err == nil || !strings.Contains(err.Error(), "Connect its GitLab or Gitea under Integrations") {
		t.Fatalf("err = %v", err)
	}
	authErr := fmt.Errorf("%w: ", transport.ErrAuthenticationRequired)
	if got := cloneCause(Request{Repo: "https://github.com/acme/api"}, authErr); !strings.HasSuffix(got, "Connect GitHub: yawl github connect") {
		t.Fatalf("github cause %q", got)
	}
	if got := cloneCause(Request{Repo: "https://github.com/acme/api", Token: "ghs_x"}, authErr); strings.Contains(got, "Connect") {
		t.Fatalf("with a token %q", got)
	}
}
