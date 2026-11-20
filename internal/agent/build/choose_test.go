package build

import (
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"os"
	"path/filepath"
	"testing"
)

func TestChoose(t *testing.T) {
	for _, c := range []struct {
		files   []string
		builder string
		want    string
	}{
		{[]string{"Dockerfile", "go.mod"}, "", "dockerfile"},
		{[]string{"go.mod"}, "", "generate"},
		{[]string{"package.json"}, "", "generate"},
		{[]string{"requirements.txt"}, "", "generate"},
		{[]string{"pyproject.toml"}, "", "generate"},
		{[]string{"Gemfile", "config.ru"}, "", "railpack"},
		{[]string{"composer.json"}, "", "railpack"},
		{[]string{"Dockerfile", "Gemfile"}, "railpack", "railpack"},
		{[]string{"package.json"}, "railpack", "railpack"},
	} {
		dir := t.TempDir()
		for _, f := range c.files {
			os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644)
		}
		if got := choose(dir, c.builder); got != c.want {
			t.Errorf("%v %q: %s, want %s", c.files, c.builder, got, c.want)
		}
	}
}

func TestCloneUser(t *testing.T) {
	for repo, want := range map[string]string{"https://gitlab.com/a/b.git": "oauth2", "https://github.com/a/b": "x-access-token"} {
		o := cloneOptions(Request{Repo: repo, Token: "t"})
		if a, ok := o.Auth.(*githttp.BasicAuth); !ok || a.Username != want || a.Password != "t" {
			t.Errorf("%s: %+v", repo, o.Auth)
		}
	}
}
