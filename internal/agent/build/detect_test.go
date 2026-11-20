package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func gen(t *testing.T, files map[string]string, keys ...string) string {
	t.Helper()
	df, err := Generate(repoWith(t, files), keys)
	if err != nil {
		t.Fatal(err)
	}
	return string(df)
}

func mustContain(t *testing.T, df string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(df, p) {
			t.Errorf("Dockerfile lacks %q:\n%s", p, df)
		}
	}
}

func TestDetectDockerfileWins(t *testing.T) {
	df, err := Generate(repoWith(t, map[string]string{"Dockerfile": "FROM x", "go.mod": "module a\n\ngo 1.23\n"}), nil)
	if err != nil || df != nil {
		t.Fatalf("Generate = %q, %v; want nil, nil", df, err)
	}
}

func TestDetectGo(t *testing.T) {
	df := gen(t, map[string]string{"go.mod": "module a\n\ngo 1.23.4\n", "main.go": "package main"})
	mustContain(t, df, "FROM golang:1.23 AS build", "go mod download", "CGO_ENABLED=0 go build -o /out/app .",
		"FROM debian:bookworm-slim", "ca-certificates", `CMD ["/app"]`)
}

func TestDetectGoProcfileBin(t *testing.T) {
	for procfile, cmd := range map[string]string{
		"web: bin/api --port $PORT\n":  `CMD ["sh","-c","/app --port $PORT"]`,
		"web: ./bin/server serve\n":    `CMD ["sh","-c","/app serve"]`,
		"web: bin/api\n":               `CMD ["sh","-c","/app"]`,
		"web: ./run.sh bin/api\n":      `CMD ["sh","-c","./run.sh bin/api"]`,
		"web: binary/api\n":            `CMD ["sh","-c","binary/api"]`,
		"web: /usr/local/bin/api -v\n": `CMD ["sh","-c","/usr/local/bin/api -v"]`,
	} {
		df := gen(t, map[string]string{"go.mod": "module a\n\ngo 1.23\n", "main.go": "package main", "Procfile": procfile})
		mustContain(t, df, cmd)
	}
}

func TestDetectNodePnpm(t *testing.T) {
	df := gen(t, map[string]string{
		"package.json":   `{"scripts":{"build":"tsc","start":"node dist/index.js"}}`,
		"pnpm-lock.yaml": "", ".nvmrc": "20.11\n",
	})
	mustContain(t, df, "FROM node:20-slim", "corepack enable && pnpm install --frozen-lockfile", "pnpm run build",
		`CMD ["sh","-c","pnpm start"]`)
}

func TestDetectNodeDefaults(t *testing.T) {
	df := gen(t, map[string]string{"package.json": `{"engines":{"node":">=18.2"},"scripts":{"start":"node index.js"}}`, "package-lock.json": "{}"})
	mustContain(t, df, "FROM node:18-slim", "npm ci", `CMD ["sh","-c","npm start"]`)
	if strings.Contains(df, "run build") {
		t.Errorf("no build script, yet a build step:\n%s", df)
	}
	df = gen(t, map[string]string{"package.json": `{"scripts":{"start":"x"}}`})
	mustContain(t, df, "FROM node:22-slim", "npm install")
}

func TestDetectNodeNoStart(t *testing.T) {
	_, err := Generate(repoWith(t, map[string]string{"package.json": `{"scripts":{"build":"tsc"}}`}), nil)
	want := `package.json has no "start" script. Add one, or a Procfile with a web: line, or a Dockerfile.`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestDetectPythonProcfile(t *testing.T) {
	df := gen(t, map[string]string{"requirements.txt": "flask\n", ".python-version": "3.11.4\n", "Procfile": "web: gunicorn app:app --bind 0.0.0.0:$PORT\nworker: celery\n"})
	mustContain(t, df, "FROM python:3.11-slim", "pip install --no-cache-dir -r requirements.txt",
		`CMD ["sh","-c","gunicorn app:app --bind 0.0.0.0:$PORT"]`)
	df = gen(t, map[string]string{"pyproject.toml": "[project]\n", "app.py": ""})
	mustContain(t, df, "FROM python:3.12-slim", "pip install --no-cache-dir .", `CMD ["sh","-c","python app.py"]`)
}

func TestDetectPythonNoStart(t *testing.T) {
	_, err := Generate(repoWith(t, map[string]string{"requirements.txt": "flask\n"}), nil)
	want := "Yawl cannot tell how to start this Python app. Add a Procfile with a web: line, or a Dockerfile."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestDetectSecretsMounted(t *testing.T) {
	df := gen(t, map[string]string{"package.json": `{"scripts":{"start":"x"}}`}, "NPM_TOKEN", "SENTRY_KEY")
	for _, line := range strings.Split(df, "\n") {
		if strings.Contains(line, "npm install") &&
			!strings.Contains(line, "--mount=type=secret,id=NPM_TOKEN,env=NPM_TOKEN --mount=type=secret,id=SENTRY_KEY,env=SENTRY_KEY") {
			t.Fatalf("install step does not mount the secrets: %s", line)
		}
	}
}

func TestDetectUnknown(t *testing.T) {
	_, err := Generate(repoWith(t, map[string]string{"index.php": "<?php"}), nil)
	want := "No Dockerfile, and Yawl could not tell the language (it knows Go, Node and Python). Add a Dockerfile."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestDetectStaticVite(t *testing.T) {
	lovable := map[string]string{
		"package.json":      `{"scripts":{"dev":"vite","build":"vite build","preview":"vite preview"},"devDependencies":{"vite":"^5","lovable-tagger":"1"}}`,
		"package-lock.json": "{}",
	}
	df := gen(t, lovable)
	mustContain(t, df, "FROM node:22-slim AS build", "npm ci", "npm run build", "FROM caddy:2-alpine",
		"COPY --from=build /app/dist /srv", "try_files {path} /index.html", ":{$PORT}")

	lovable["package.json"] = `{"scripts":{"build":"vite build","start":"node server.js"},"devDependencies":{"vite":"^5"}}`
	if df := gen(t, lovable); strings.Contains(df, "caddy") || !strings.Contains(df, `CMD ["sh","-c","npm start"]`) {
		t.Errorf("a Vite app with its own server built as a static site:\n%s", df)
	}
	lovable["package.json"] = `{"scripts":{"build":"vite build","start":"vite preview --port 3000"}}`
	lovable["vite.config.ts"] = "export default {}"
	mustContain(t, gen(t, lovable), "FROM caddy:2-alpine")
	lovable["Procfile"] = "web: node server.js\n"
	if df := gen(t, lovable); strings.Contains(df, "caddy") {
		t.Errorf("a Procfile web: line still built static:\n%s", df)
	}
}

func TestDetectReplit(t *testing.T) {
	base := `{"scripts":{"build":"vite build && esbuild server/index.ts"}}`
	df := gen(t, map[string]string{"package.json": base,
		".replit": "run = \"npm run dev\"\nmodules = [\"nodejs-20\"]\n\n[deployment]\ndeploymentTarget = \"autoscale\"\nbuild = \"npm run build:prod\"\nrun = [\"npm\", \"run\", \"start\"]\n\n[[ports]]\nlocalPort = 5000\nexternalPort = 80\n"})
	mustContain(t, df, `CMD ["sh","-c","npm run start"]`, "RUN npm run build:prod")
	if strings.Contains(df, "RUN npm run build\n") {
		t.Errorf(".replit build did not replace the build script:\n%s", df)
	}
	mustContain(t, gen(t, map[string]string{"package.json": base, ".replit": "[deployment]\nrun = \"node dist/index.js\"\n"}), `CMD ["sh","-c","node dist/index.js"]`)
	mustContain(t, gen(t, map[string]string{"package.json": base, ".replit": "run = \"node index.js\"\n"}), `CMD ["sh","-c","node index.js"]`)
	mustContain(t, gen(t, map[string]string{"main.py": "print(1)", "requirements.txt": "", ".replit": "[deployment]\nrun = [\"python3\", \"main.py\"]\n"}), `CMD ["sh","-c","python3 main.py"]`)

	r, err := Inspect(context.Background(), Request{Repo: repoOf(t, map[string]string{"package.json": base, "server/index.ts": "app.listen(5000)\n",
		".replit": "[deployment]\nrun = [\"npm\", \"run\", \"start\"]\n\n[[ports]]\nlocalPort = 5000\nexternalPort = 80\n\n[[ports]]\nlocalPort = 24678\n"})})
	if err != nil || r.Port != 5000 || r.ReadsPort || r.Static {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestDetectBunOnly(t *testing.T) {
	df := gen(t, map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"5"}}`, "bun.lockb": "x"})
	mustContain(t, df, "RUN npm install\n")
	for _, lock := range []string{"bun.lockb", "bun.lock"} {
		df := gen(t, map[string]string{"package.json": `{"scripts":{"start":"node index.js"}}`, lock: "x", "package-lock.json": "{}"})
		mustContain(t, df, "RUN npm install\n")
	}
}

func TestInspectStatic(t *testing.T) {
	r, err := Inspect(context.Background(), Request{Repo: repoOf(t, map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"5","lovable-tagger":"1"}}`})})
	if err != nil || !r.Static || !r.ReadsPort || r.Builder != "Lovable" || r.HealthPath != "/" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestDetectSvelteKitNotStatic(t *testing.T) {
	for _, dep := range []string{"@sveltejs/kit", "@react-router/dev"} {
		_, err := Generate(repoWith(t, map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"5","` + dep + `":"2"}}`}), nil)
		if err == nil || !strings.Contains(err.Error(), `package.json has no "start" script`) {
			t.Fatalf("%s: err = %v", dep, err)
		}
	}
}

func TestDetectViteDevStartIsStatic(t *testing.T) {
	for _, start := range []string{"vite", "vite --port 3000", "vite preview"} {
		mustContain(t, gen(t, map[string]string{"package.json": `{"scripts":{"build":"vite build","start":"` + start + `"},"devDependencies":{"vite":"5"}}`}), "FROM caddy:2-alpine")
	}
	if df := gen(t, map[string]string{"package.json": `{"scripts":{"build":"vite build","start":"vite-node server.ts"},"devDependencies":{"vite":"5"}}`}); strings.Contains(df, "caddy") {
		t.Errorf("vite-node served as files:\n%s", df)
	}
}

func TestDetectStaticCaddyfile(t *testing.T) {
	df := gen(t, map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"5"}}`})
	mustContain(t, df, `printf ':{$PORT}\nroot * /srv\nhandle /assets/* {\n\tfile_server\n}\nhandle {\n\theader Cache-Control no-cache\n\ttry_files {path} /index.html\n\tfile_server\n}\n' > /etc/caddy/Caddyfile`)
}
