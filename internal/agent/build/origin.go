package build

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var errExpo = errors.New("This is an Expo app for phones. Yawl runs web apps and servers.")

var v0Name = regexp.MustCompile(`"name"\s*:\s*"my-v0-project"`)

func builder(dir string) string {
	pkg := read(dir, "package.json")
	switch {
	case strings.Contains(pkg, `"lovable-tagger"`):
		return "Lovable"
	case exists(dir, ".bolt"):
		return "Bolt"
	case exists(dir, ".replit"):
		return "Replit"
	case v0Name.MatchString(pkg):
		return "v0"
	}
	return ""
}

func deps(dir string) map[string]bool {
	var pkg struct{ Dependencies, DevDependencies map[string]string }
	json.Unmarshal([]byte(read(dir, "package.json")), &pkg)
	out := map[string]bool{}
	for k := range pkg.Dependencies {
		out[k] = true
	}
	for k := range pkg.DevDependencies {
		out[k] = true
	}
	return out
}

func expo(dir string) bool {
	d := deps(dir)
	return d["expo"] && !d["vite"] && !d["next"]
}

func isStatic(dir string) bool {
	var pkg nodePkg
	if json.Unmarshal([]byte(read(dir, "package.json")), &pkg) != nil || pkg.Scripts["build"] == "" {
		return false
	}
	d := deps(dir)
	if d["@sveltejs/kit"] || d["@react-router/dev"] {
		return false
	}
	vite := d["vite"] || exists(dir, "vite.config.js") || exists(dir, "vite.config.ts") || exists(dir, "vite.config.mjs")
	start := pkg.Scripts["start"]
	ownServer := (start != "" && !viteServes(start)) || procfileWeb(dir) != "" || replitDeploys(dir)
	return vite && !ownServer
}

func viteServes(start string) bool {
	return start == "vite" || strings.HasPrefix(start, "vite ")
}

func replit(dir string) (run, build string, port int, deployed bool) {
	var f struct {
		Run        any `toml:"run"`
		Deployment struct {
			Run   any `toml:"run"`
			Build any `toml:"build"`
		} `toml:"deployment"`
		Ports []struct {
			LocalPort int `toml:"localPort"`
		} `toml:"ports"`
	}
	toml.Unmarshal([]byte(read(dir, ".replit")), &f)
	if len(f.Ports) > 0 {
		port = f.Ports[0].LocalPort
	}
	run, build = shellCommand(f.Deployment.Run), shellCommand(f.Deployment.Build)
	deployed = run != ""
	if !deployed {
		run = shellCommand(f.Run)
	}
	return run, build, port, deployed
}

func replitDeploys(dir string) bool {
	_, _, _, deployed := replit(dir)
	return deployed
}

func shellCommand(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, a := range v {
			s, _ := a.(string)
			if s == "" || strings.ContainsFunc(s, func(r rune) bool { return !strings.ContainsRune(plainChars, r) }) {
				s = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
			}
			out = append(out, s)
		}
		return strings.Join(out, " ")
	}
	return ""
}

const plainChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:=@%+-"

func notes(dir string) []string {
	out := []string{}
	if deps(dir)["@supabase/supabase-js"] {
		out = append(out, "Supabase stays where it is. Add this service's address in Supabase, Authentication, URL Configuration, or sign in sends people back to the old address.")
	}
	if exists(dir, "supabase/functions") {
		out = append(out, "Functions in supabase/functions stay on Supabase; Yawl does not deploy them.")
	}
	return out
}

var replitKeys = map[string]bool{"REPLIT_DB_URL": true, "REPL_ID": true, "REPL_SLUG": true, "REPL_OWNER": true, "REPLIT_DOMAINS": true, "REPLIT_DEV_DOMAIN": true}

func replitOnly(dir string, keys []string) []string {
	var out []string
	for _, k := range keys {
		if replitKeys[k] {
			out = append(out, k)
		}
	}
	if deps(dir)["@replit/database"] {
		out = append(out, "@replit/database")
	}
	return out
}

func replitWarning(dir string, keys []string) []string {
	if used := replitOnly(dir, keys); len(used) > 0 {
		return []string{"This app uses Replit's own services (" + strings.Join(used, ", ") + "), which work only on Replit."}
	}
	return nil
}

func ownedBy(k string) bool { return ambient[k] || replitKeys[k] }
