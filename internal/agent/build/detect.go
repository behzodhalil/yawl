package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func choose(dir, builder string) string {
	switch {
	case builder == "railpack":
		return "railpack"
	case exists(dir, "Dockerfile"):
		return "dockerfile"
	case exists(dir, "go.mod") || exists(dir, "package.json") || exists(dir, "requirements.txt") || exists(dir, "pyproject.toml"):
		return "generate"
	}
	return "railpack"
}

func Generate(dir string, secretKeys []string) ([]byte, error) {
	if exists(dir, "Dockerfile") {
		return nil, nil
	}
	mounts := ""
	for _, k := range secretKeys {
		mounts += fmt.Sprintf("--mount=type=secret,id=%s,env=%s ", k, k)
	}
	web := procfileWeb(dir)
	run, _, _, deployed := replit(dir)
	last := ""
	if deployed && web == "" {
		web = run
	} else if !deployed {
		last = run
	}
	var df string
	var err error
	switch {
	case exists(dir, "go.mod"):
		df, err = goDockerfile(dir, mounts, procfileWeb(dir))
	case exists(dir, "package.json") && expo(dir):
		err = errExpo
	case exists(dir, "package.json") && isStatic(dir):
		df, err = staticDockerfile(dir, mounts)
	case exists(dir, "package.json"):
		df, err = nodeDockerfile(dir, mounts, web, last)
	case exists(dir, "requirements.txt") || exists(dir, "pyproject.toml"):
		df, err = pythonDockerfile(dir, mounts, web, last)
	default:
		err = errors.New("No Dockerfile, and Yawl could not tell the language (it knows Go, Node and Python). Add a Dockerfile.")
	}
	return []byte(df), err
}

var goDirective = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+)`)

var herokuBin = regexp.MustCompile(`^(?:\./)?bin/\S+`)

func goDockerfile(dir, mounts, web string) (string, error) {
	ver := "1"
	if m := goDirective.FindStringSubmatch(read(dir, "go.mod")); m != nil {
		ver = string(m[1])
	}
	cmd := `["/app"]`
	if web != "" {
		cmd = shell(herokuBin.ReplaceAllString(web, "/app")) // bin/<name> is Heroku's build output; Yawl's is /app (0.1.2, owner chose B)
	}
	return "FROM golang:" + ver + " AS build\nWORKDIR /src\nCOPY go.mod go.sum* ./\n" +
		"RUN " + mounts + "go mod download\nCOPY . .\n" +
		"RUN " + mounts + "CGO_ENABLED=0 go build -o /out/app .\n\n" +
		"FROM debian:bookworm-slim\n" +
		"RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*\n" +
		"WORKDIR /srv\nCOPY --from=build /out/app /app\nCMD " + cmd + "\n", nil
}

var leadingDigits = regexp.MustCompile(`\d+`)

type nodePkg struct {
	Scripts map[string]string     `json:"scripts"`
	Engines struct{ Node string } `json:"engines"`
}

func nodeSetup(dir string) (pkg nodePkg, major, pm, install, build string, err error) {
	if err = json.Unmarshal([]byte(read(dir, "package.json")), &pkg); err != nil {
		return pkg, "", "", "", "", fmt.Errorf("package.json: %v", err)
	}
	major = "22"
	if nvmrc := read(dir, ".nvmrc"); leadingDigits.MatchString(nvmrc) {
		major = leadingDigits.FindString(nvmrc)
	} else if pkg.Engines.Node != "" && leadingDigits.MatchString(pkg.Engines.Node) {
		major = leadingDigits.FindString(pkg.Engines.Node)
	}
	pm, install = "npm", "npm install"
	switch {
	case exists(dir, "pnpm-lock.yaml"):
		pm, install = "pnpm", "corepack enable && pnpm install --frozen-lockfile"
	case exists(dir, "yarn.lock"):
		pm, install = "yarn", "corepack enable && yarn install --frozen-lockfile"
	case exists(dir, "package-lock.json") && !exists(dir, "bun.lockb") && !exists(dir, "bun.lock"): // beside Bun's lock it is often stale, and npm ci refuses it (24-app-import M6)
		install = "npm ci"
	}
	if pkg.Scripts["build"] != "" {
		build = pm + " run build"
	}
	if _, rb, _, _ := replit(dir); rb != "" {
		build = rb
	}
	return pkg, major, pm, install, build, nil
}

func nodeDockerfile(dir, mounts, web, last string) (string, error) {
	pkg, major, pm, install, build, err := nodeSetup(dir)
	if err != nil {
		return "", err
	}
	start := web
	if start == "" && pkg.Scripts["start"] != "" {
		start = pm + " start"
	}
	if start == "" {
		start = last
	}
	if start == "" {
		return "", errors.New(`package.json has no "start" script. Add one, or a Procfile with a web: line, or a Dockerfile.`)
	}
	df := "FROM node:" + major + "-slim\nWORKDIR /app\nCOPY . .\nRUN " + mounts + install + "\n"
	if build != "" {
		df += "RUN " + mounts + build + "\n"
	}
	return df + "CMD " + shell(start) + "\n", nil
}

func staticDockerfile(dir, mounts string) (string, error) {
	_, major, _, install, build, err := nodeSetup(dir)
	if err != nil {
		return "", err
	}
	return "FROM node:" + major + "-slim AS build\nWORKDIR /app\nCOPY . .\nRUN " + mounts + install + "\nRUN " + mounts + build + "\n\n" +
		"FROM caddy:2-alpine\nCOPY --from=build /app/dist /srv\n" +
		`RUN printf ':{$PORT}\nroot * /srv\nhandle /assets/* {\n\tfile_server\n}\nhandle {\n\theader Cache-Control no-cache\n\ttry_files {path} /index.html\n\tfile_server\n}\n' > /etc/caddy/Caddyfile` + "\n", nil
}

func pythonDockerfile(dir, mounts, web, last string) (string, error) {
	ver := "3.12"
	if m := majorMinor(read(dir, ".python-version")); m != "" {
		ver = m
	}
	install := "pip install --no-cache-dir ."
	if exists(dir, "requirements.txt") {
		install = "pip install --no-cache-dir -r requirements.txt"
	}
	start := web
	for _, f := range []string{"main.py", "app.py"} {
		if start == "" && exists(dir, f) {
			start = "python " + f
		}
	}
	if start == "" {
		start = last
	}
	if start == "" {
		return "", errors.New("Yawl cannot tell how to start this Python app. Add a Procfile with a web: line, or a Dockerfile.")
	}
	return "FROM python:" + ver + "-slim\nWORKDIR /app\nCOPY . .\nRUN " + mounts + install + "\nCMD " + shell(start) + "\n", nil
}

func procfileWeb(dir string) string {
	for _, line := range strings.Split(read(dir, "Procfile"), "\n") {
		if cmd, ok := strings.CutPrefix(strings.TrimSpace(line), "web:"); ok {
			return strings.TrimSpace(cmd)
		}
	}
	return ""
}

func shell(cmd string) string {
	b, _ := json.Marshal([]string{"sh", "-c", cmd})
	return string(b)
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}
