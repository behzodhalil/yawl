package spec

import "testing"

func TestRepoKey(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/Acme/Shop.git":     "acme/shop",
		"git@github.com:acme/shop":             "acme/shop",
		"https://gitlab.com/team/sub/shop.git": "gitlab.com/team/sub/shop",
		"https://git.acme.uz/a/b":              "git.acme.uz/a/b",
		"https://gitlab.com/Team/Shop/":        "gitlab.com/team/shop",
		"file:///x":                            "",
		"https://gitlab.com/onlyone":           "",
		"http://gitlab.example/a/b":            "",
	} {
		got, ok := RepoKey(url)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q %v, want %q", url, got, ok, want)
		}
	}
}
