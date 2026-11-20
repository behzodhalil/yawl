package build

import (
	"context"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Report struct {
	Commit     string
	Dockerfile bool
	Language   string
	ReadsPort  bool
	Port       int
	HealthPath string
	Missing    []string
	Warnings   []string
	Builder    string
	Static     bool
	Notes      []string
}

var inspectLimit = 2 * time.Minute

func Inspect(ctx context.Context, r Request) (Report, error) {
	ctx, cancel := context.WithTimeout(ctx, inspectLimit)
	defer cancel()
	r.Depth = 1
	dir, head, err := clone(ctx, r)
	if err != nil {
		if r.Token != "" {
			err = redactErr(err, r.Token)
		}
		return Report{}, err
	}
	defer os.RemoveAll(dir)
	rep := Report{Commit: head.Commit, Dockerfile: exists(dir, "Dockerfile"), Missing: []string{}, Warnings: []string{}, HealthPath: "/",
		Builder: builder(dir), Notes: notes(dir)}
	if !rep.Dockerfile && expo(dir) {
		return Report{}, errExpo
	}
	if rep.Dockerfile {
		if m := exposeRe.FindStringSubmatch(read(dir, "Dockerfile")); m != nil {
			rep.Port, _ = strconv.Atoi(string(m[1]))
		}
	} else {
		rep.Language = detectedLanguage(dir)
	}
	committed := committedKeys(dir)
	keys := readKeys(dir)
	for _, k := range keys {
		switch {
		case k == "PORT":
			rep.ReadsPort = true
		case !slices.Contains(r.Keys, k) && !ownedBy(k) && !committed[k]:
			rep.Missing = append(rep.Missing, k)
		}
	}
	rep.Warnings = append(rep.Warnings, replitWarning(dir, keys)...)
	if !rep.Dockerfile && isStatic(dir) {
		rep.Static, rep.ReadsPort = true, true
	}
	if _, _, port, _ := replit(dir); rep.Port == 0 && !rep.ReadsPort {
		rep.Port = port
	}
	rep.Warnings = append(rep.Warnings, versions(dir, !rep.Dockerfile)...)
	rep.HealthPath = healthPath(dir)
	return rep, nil
}

var exposeRe = regexp.MustCompile(`(?im)^\s*EXPOSE\s+(\d+)`)

func detectedLanguage(dir string) string {
	var lang, key string
	switch {
	case exists(dir, "go.mod"):
		lang, key = "go", "golang"
	case exists(dir, "package.json"):
		lang, key = "node", "node"
	case exists(dir, "requirements.txt") || exists(dir, "pyproject.toml"):
		lang, key = "python", "python"
	default:
		return ""
	}
	if v := built(dir, true, key); v != "" {
		return lang + " " + v
	}
	return lang
}

var healthPaths = []string{"/health", "/healthz", "/up", "/status"}

func healthPath(dir string) string {
	found := map[string]bool{}
	scanCode(dir, func(b []byte) {
		for _, p := range healthPaths {
			for _, q := range []string{`"`, `'`, "`"} {
				if strings.Contains(string(b), q+p+q) {
					found[p] = true
				}
			}
		}
	})
	for _, p := range healthPaths {
		if found[p] {
			return p
		}
	}
	return "/"
}

func redactErr(err error, token string) error {
	return errorString(strings.ReplaceAll(err.Error(), token, "••••"))
}

type errorString string

func (e errorString) Error() string { return string(e) }
