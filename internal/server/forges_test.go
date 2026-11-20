package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/gitea"
	"github.com/behzodhalil/yawl/internal/server/github"
	"github.com/behzodhalil/yawl/internal/server/gitlab"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

func TestForgeTokens(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) }))
	defer fake.Close()
	old, oldGt := gitlab.APIBase, gitea.APIBase
	gitlab.APIBase = func(string) string { return fake.URL }
	gitea.APIBase = func(string) string { return fake.URL }
	defer func() { gitlab.APIBase, gitea.APIBase = old, oldGt }()
	st, err := store.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, _ := vault.LoadOrCreate(t.TempDir())
	ctx := context.Background()
	if _, err := gitlab.Connect(ctx, st.Q, v, "gitlab.com", "glpat-x"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitea.Connect(ctx, st.Q, v, "codeberg.org", "gt-x"); err != nil {
		t.Fatal(err)
	}
	f := forgeTokens{gh: github.Tokens{Q: st.Q, V: v}, gl: gitlab.Tokens{Q: st.Q, V: v}, gt: gitea.Tokens{Q: st.Q, V: v}}
	for repo, want := range map[string]string{"https://gitlab.com/a/b": "glpat-x", "https://github.com/a/b": "", "https://git.other.uz/a/b": "", "https://codeberg.org/a/b": "gt-x"} {
		if got, err := f.Token(ctx, repo); err != nil || got != want {
			t.Errorf("%s: %q %v", repo, got, err)
		}
	}
}
