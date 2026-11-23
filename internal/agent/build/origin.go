package build

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
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

// replit reads .replit: the start command ([deployment] run, else the top
// level run, which is Replit's Run button and often a dev server; deployed
// says which), the [deployment] build, and the first [[ports]] localPort.
func replit(dir string) (run, build string, port int, deployed bool) {
	var top string
	section := ""
	for _, line := range strings.Split(read(dir, ".replit"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), tomlValue(strings.TrimSpace(v))
		switch {
		case section == "" && k == "run":
			top = v
		case section == "[deployment]" && k == "run":
			run = v
		case section == "[deployment]" && k == "build":
			build = v
		case section == "[[ports]]" && k == "localPort" && port == 0:
			port, _ = strconv.Atoi(v)
		}
	}
	deployed = run != ""
	if !deployed {
		run = top
	}
	return run, build, port, deployed
}

func replitDeploys(dir string) bool {
	_, _, _, deployed := replit(dir)
	return deployed
}

// tomlValue is a string, a number, or an array of strings joined by spaces.
// Only the first value is read: a # comment may follow it.
func tomlValue(v string) string {
	first := func(out any) bool { return json.NewDecoder(strings.NewReader(v)).Decode(out) == nil }
	var s string
	if first(&s) {
		return s
	}
	var arr []string
	if first(&arr) {
		for i, a := range arr {
			if a == "" || strings.ContainsFunc(a, func(r rune) bool { return !strings.ContainsRune(plainChars, r) }) {
				arr[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
			}
		}
		return strings.Join(arr, " ")
	}
	v, _, _ = strings.Cut(v, "#")
	return strings.TrimSpace(v)
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
