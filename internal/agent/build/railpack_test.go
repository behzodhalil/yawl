package build

import (
	"context"
	"strings"
	"testing"
)

func TestRailpackPrepareDefinition(t *testing.T) {
	env := map[string][]byte{"DB_CONNECTION": []byte("pgsql"), "DATABASE_URL": []byte("postgres://secret")}
	for arch, want := range map[string][]string{
		"amd64": {"https://github.com/railwayapp/railpack/releases/download/v0.43.0/railpack-v0.43.0-x86_64-unknown-linux-musl.tar.gz", "sha256:248a8f60f36854aabed6124113631078a802de3461660855fc4de9c26065c1fb"},
		"arm64": {"https://github.com/railwayapp/railpack/releases/download/v0.43.0/railpack-v0.43.0-arm64-unknown-linux-musl.tar.gz", "sha256:732ece14eb4f4676a43ebba19431f954edf7b130acb76159ef0f2ff86ac42ac9"},
	} {
		def, err := prepareState(env, arch).Marshal(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var all strings.Builder
		for _, d := range def.Def {
			all.Write(d)
		}
		s := all.String()
		for _, w := range append(want, "ghcr.io/railwayapp/railpack-builder:mise-2026.10.14@sha256:03df9e9aa1e786c2e0dd12c5409acad3bbd5a0fa8a75c1117c7ddab5c0c3fde4", "local://context", "DB_CONNECTION", "DATABASE_URL",
			"YAWL_SECRETS_HASH="+secretsHash(env), `exec /opt/yawl-railpack/railpack prepare /app --plan-out /out/railpack-plan.json "${a[@]}"`) {
			if !strings.Contains(s, w) {
				t.Errorf("%s: lacks %q", arch, w)
			}
		}
		if strings.Contains(s, "postgres://secret") || strings.Contains(s, "pgsql") {
			t.Fatalf("%s: a value is in the plan step", arch)
		}
	}
	if _, _, err := prepareArch("riscv64"); err == nil {
		t.Fatal("an arch with no pinned release was accepted")
	}
}

func TestSecretsHash(t *testing.T) {
	a := secretsHash(map[string][]byte{"A": []byte("1"), "B": []byte("2")})
	if a != secretsHash(map[string][]byte{"B": []byte("2"), "A": []byte("1")}) || a == secretsHash(map[string][]byte{"A": []byte("1"), "B": []byte("3")}) {
		t.Fatal("hash")
	}
}

func TestRailpackFrontendAttrs(t *testing.T) {
	got := railpackAttrs("h1")
	want := map[string]string{"source": "ghcr.io/railwayapp/railpack-frontend:v0.43.0@sha256:355ef61fae6f2551103cd8b10c710cb07167dd353d561f15c69b8d7ac1c676cc", "filename": "railpack-plan.json",
		"build-arg:secrets-hash": "h1"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: %q", k, got[k])
		}
	}
}

func TestNoCacheAttrs(t *testing.T) {
	if _, ok := dockerfileAttrs("sha", true)["no-cache"]; !ok {
		t.Fatal("dockerfile")
	}
	if _, ok := dockerfileAttrs("sha", false)["no-cache"]; ok {
		t.Fatal("dockerfile without")
	}

}

func TestRailpackNoCache(t *testing.T) {
	a := railpackNoCache(railpackAttrs("h"), true, "yawl/acme-api:dep_9")
	if a["build-arg:cache-key"] != "yawl/acme-api:dep_9" {
		t.Fatalf("%v", a)
	}
	if _, ok := railpackNoCache(railpackAttrs("h"), false, "x")["build-arg:cache-key"]; ok {
		t.Fatal("a cache key without no-cache")
	}
}
