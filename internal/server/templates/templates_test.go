package templates

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/spec"
)

func TestTemplatesParse(t *testing.T) {
	shellVar := regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)`)
	var ids []string
	for _, tp := range All() {
		ids = append(ids, tp.ID)
		names, dbs := map[string]bool{}, map[string]bool{}
		for _, s := range tp.Services {
			names[s.Name] = true
			if spec.IsDatabase(s.Kind) {
				dbs[s.Name] = true
			}
			if s.Memory == 0 {
				t.Errorf("%s/%s has no memory limit", tp.ID, s.Name)
			}
		}
		keys := map[string]bool{}
		for _, v := range tp.Vars {
			keys[v.Service+"/"+v.Key] = true
			if !names[v.Service] || !slices.Contains([]string{"fixed", "generate", "ask", "ref"}, v.Kind) {
				t.Errorf("%s: var %+v", tp.ID, v)
			}
			if db, ok := strings.CutSuffix(v.Value, ".url"); v.Kind == "ref" && (!ok || !dbs[db]) {
				t.Errorf("%s: %s refers to %s, not a database of the template", tp.ID, v.Key, v.Value)
			}
			if v.Kind == "ask" && v.Label == "" {
				t.Errorf("%s: %s asks without a question", tp.ID, v.Key)
			}
		}
		for _, s := range tp.Services {
			for _, arg := range s.Command {
				for _, m := range shellVar.FindAllStringSubmatch(arg, -1) {
					if !keys[s.Name+"/"+m[1]] && !strings.Contains(arg, m[1]+"=") {
						t.Errorf("%s/%s: the command reads $%s, which the template does not set", tp.ID, s.Name, m[1])
					}
				}
			}
		}
		if tp.Title == "" || tp.Line == "" || tp.Memory == "" || tp.Note == "" {
			t.Errorf("%+v", tp)
		}
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"directus", "ghost", "gitea", "grafana-loki", "listmonk", "meilisearch", "metabase", "n8n", "nocodb", "plausible", "umami", "uptime-kuma", "vaultwarden"}) {
		t.Fatal(ids)
	}
	g, ok := Get("grafana-loki")
	if !ok || len(g.Services) != 2 || g.Services[0].Image != "grafana/grafana:11.6" || g.Services[1].Kind != spec.KindWorker || g.Memory != "1GB" {
		t.Fatalf("%+v", g)
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("unknown id found")
	}
}

func TestTemplateIcons(t *testing.T) {
	bad := regexp.MustCompile(`(?i)<script|\son[a-z]+=|<foreignobject|href="(https?:|javascript:|//)`)
	for _, tp := range All() {
		if len(tp.Icons) == 0 {
			t.Errorf("%s has no icon", tp.ID)
		}
		for _, svg := range tp.Icons {
			if !strings.HasPrefix(strings.TrimSpace(svg), "<svg") || bad.MatchString(svg) {
				t.Errorf("%s: unsafe or not an svg: %.80s", tp.ID, svg)
			}
		}
	}
	if g, _ := Get("grafana-loki"); len(g.Icons) != 2 {
		t.Fatalf("grafana-loki icons %d", len(g.Icons))
	}
}

func TestTemplateImages(t *testing.T) {
	want := map[string]string{"grafana": "grafana/grafana:11.6", "loki": "grafana/loki:3", "n8n": "n8nio/n8n:2.46.6",
		"uptime-kuma": "louislam/uptime-kuma:2.5.9", "umami": "ghcr.io/umami-software/umami:postgresql-v2.20.2",
		"plausible": "ghcr.io/plausible/community-edition:v3.2.1", "ghost": "ghost:6.75.0", "metabase": "metabase/metabase:v0.65.3.3",
		"gitea": "gitea/gitea:28.3.0", "vaultwarden": "vaultwarden/server:1.37.5", "listmonk": "listmonk/listmonk:v6.2.0",
		"directus": "directus/directus:12.6.1", "nocodb": "nocodb/nocodb:2026.10.1", "meilisearch": "getmeili/meilisearch:v1.56.1"}
	floating := []string{"", "latest", "stable", "main", "master", "nightly", "next", "beta", "edge", "dev"}
	for _, tp := range All() {
		for _, s := range tp.Services {
			if s.Image == "" {
				continue
			}
			if w := want[s.Name]; s.Image != w {
				t.Errorf("%s: %s, want %s", s.Name, s.Image, w)
			}
			tag := strings.TrimPrefix(s.Image, spec.ImageName(s.Image))
			if tag = strings.TrimPrefix(tag, ":"); slices.Contains(floating, tag) {
				t.Errorf("%s: %s does not pin a version", s.Name, s.Image)
			}
		}
	}
}
