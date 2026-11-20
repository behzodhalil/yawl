package build

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func Warnings(dir string, keys []string, env string, uses string) []string {
	var out []string
	committed := committedKeys(dir)
	read := readKeys(dir)
	for _, k := range read {
		if !slices.Contains(keys, k) && !ownedBy(k) && !committed[k] {
			out = append(out, fmt.Sprintf("The code reads %s, which is not set in %s.", k, env))
		}
	}
	out = append(out, replitWarning(dir, read)...)
	if uses != "railpack" { // it reads .nvmrc, engines and the rest as it plans
		out = append(out, versions(dir, uses == "generate")...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

var ambient = map[string]bool{"PORT": true, "NODE_ENV": true, "HOME": true, "PATH": true, "HOSTNAME": true, "PWD": true}

// viteOwn are set by Vite itself, and only in import.meta.env.
var viteOwn = map[string]bool{"MODE": true, "DEV": true, "PROD": true, "BASE_URL": true, "SSR": true}

var importMetaRe = regexp.MustCompile(`import\.meta\.env\.([A-Za-z_][A-Za-z0-9_]*)`)

// committedKeys are the keys with a value in a committed .env or
// .env.production: Vite and Next read those files at build time.
func committedKeys(dir string) map[string]bool {
	out := map[string]bool{}
	for _, f := range []string{".env", ".env.production"} {
		for _, line := range strings.Split(read(dir, f), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && !strings.HasPrefix(k, "#") && strings.Trim(strings.TrimSpace(v), `"'`) != "" {
				out[strings.TrimSpace(strings.TrimPrefix(k, "export "))] = true
			}
		}
	}
	return out
}

var keyRes = []*regexp.Regexp{
	regexp.MustCompile(`process\.env\.([A-Za-z_][A-Za-z0-9_]*)`),
	regexp.MustCompile(`process\.env\[["']([A-Za-z_][A-Za-z0-9_]*)["']\]`),
	regexp.MustCompile(`os\.environ\[["']([A-Za-z_][A-Za-z0-9_]*)["']\]`),
	regexp.MustCompile(`os\.environ\.get\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']`),
	regexp.MustCompile(`os\.getenv\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']`),
	regexp.MustCompile(`os\.Getenv\(\s*"([A-Za-z_][A-Za-z0-9_]*)"`),
}

var codeExt = []string{".js", ".ts", ".jsx", ".tsx", ".mjs", ".cjs", ".py", ".go"}

const (
	maxScanFiles = 5000
	maxScanSize  = 1 << 20
)

func readKeys(dir string) []string {
	seen := map[string]bool{}
	scanCode(dir, func(b []byte) {
		for _, re := range keyRes {
			for _, m := range re.FindAllSubmatch(b, -1) {
				seen[string(m[1])] = true
			}
		}
		for _, m := range importMetaRe.FindAllSubmatch(b, -1) {
			if !viteOwn[string(m[1])] {
				seen[string(m[1])] = true
			}
		}
	})
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func scanCode(dir string, fn func([]byte)) {
	n := 0
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "vendor", ".git", ".venv", "venv":
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if !slices.Contains(codeExt, filepath.Ext(p)) {
			return nil
		}
		if n++; n > maxScanFiles {
			return filepath.SkipAll
		}
		if fi, err := d.Info(); err != nil || fi.Size() > maxScanSize {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil {
			fn(b)
		}
		return nil
	})
}

var fromRe = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--\S+\s+)*(?:docker\.io/)?(?:library/)?(node|python|golang):(\d+(?:\.\d+)?)`)

func built(dir string, detected bool, lang string) string {
	if !detected {
		for _, m := range fromRe.FindAllStringSubmatch(read(dir, "Dockerfile"), -1) {
			if m[1] == lang {
				return m[2]
			}
		}
		return ""
	}
	switch lang {
	case "node":
		if v := firstNum(read(dir, ".nvmrc")); v != "" {
			return v
		}
		if v := firstNum(enginesNode(dir)); v != "" {
			return v
		}
		return "22"
	case "python":
		if v := majorMinor(read(dir, ".python-version")); v != "" {
			return v
		}
		return "3.12"
	case "golang":
		return majorMinor(goDirectiveOf(dir))
	}
	return ""
}

func versions(dir string, detected bool) []string {
	var out []string
	say := func(file, name, want, uses string) {
		out = append(out, fmt.Sprintf("%s asks for %s %s; the build uses %s %s.", file, name, want, name, uses))
	}
	if uses := built(dir, detected, "node"); uses != "" {
		if e := enginesNode(dir); firstNum(e) != "" {
			want := firstNum(e)
			if strings.HasPrefix(strings.TrimSpace(e), ">=") && !strings.Contains(e, "<") {
				if less(uses, want) {
					say("package.json", "Node", want, uses)
				}
			} else if firstNum(uses) != want {
				say("package.json", "Node", want, uses)
			}
		}
		if want := firstNum(read(dir, ".nvmrc")); want != "" && firstNum(uses) != want {
			say(".nvmrc", "Node", want, uses)
		}
	}
	if uses := built(dir, detected, "python"); uses != "" {
		if want := majorMinor(read(dir, ".python-version")); want != "" && want != majorMinor(uses) {
			say(".python-version", "Python", want, uses)
		}
	}
	if uses := built(dir, detected, "golang"); uses != "" {
		if want := majorMinor(goDirectiveOf(dir)); want != "" && less(uses, want) { // Go builds older code fine
			say("go.mod", "Go", want, uses)
		}
	}
	return out
}

func read(dir, name string) string {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ""
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() || fi.Size() > maxScanSize {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(f, maxScanSize))
	return string(b)
}

func enginesNode(dir string) string {
	var pkg struct {
		Engines struct{ Node string } `json:"engines"`
	}
	json.Unmarshal([]byte(read(dir, "package.json")), &pkg)
	return pkg.Engines.Node
}

func goDirectiveOf(dir string) string {
	if m := goDirective.FindStringSubmatch(read(dir, "go.mod")); m != nil {
		return m[1]
	}
	return ""
}

var majorMinorRe = regexp.MustCompile(`\d+\.\d+`)

func firstNum(s string) string   { return leadingDigits.FindString(s) }
func majorMinor(s string) string { return majorMinorRe.FindString(s) }

func less(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}
