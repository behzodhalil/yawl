package build

import (
	"context"
	"reflect"
	"testing"

	"github.com/behzodhalil/yawl/internal/spec"
)

func TestBuilderMarks(t *testing.T) {
	cases := map[string]map[string]string{
		"Lovable": {"package.json": `{"devDependencies":{"lovable-tagger":"^1.1.7"}}`},
		"Bolt":    {"package.json": `{}`, ".bolt/prompt": "x"},
		"Replit":  {".replit": "run = \"npm run dev\"\n"},
		"v0":      {"package.json": `{"name": "my-v0-project","scripts":{"start":"next start"}}`},
		"":        {"package.json": `{"name":"api"}`},
	}
	for want, files := range cases {
		if got := builder(tree(t, files)); got != want {
			t.Errorf("builder = %q, want %q", got, want)
		}
	}
}

func TestSupabaseNotes(t *testing.T) {
	dir := tree(t, map[string]string{
		"package.json":                      `{"dependencies":{"@supabase/supabase-js":"^2"},"devDependencies":{"lovable-tagger":"1"}}`,
		"supabase/functions/hello/index.ts": "Deno.serve(() => new Response('hi'))\n",
	})
	want := []string{
		"Supabase stays where it is. Add this service's address in Supabase, Authentication, URL Configuration, or sign in sends people back to the old address.",
		"Functions in supabase/functions stay on Supabase; Yawl does not deploy them.",
	}
	if got := notes(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("notes %q", got)
	}
	if got := notes(tree(t, map[string]string{"package.json": `{}`})); len(got) != 0 {
		t.Fatalf("plain repo notes %q", got)
	}
}

func TestReplitKeysWarn(t *testing.T) {
	files := map[string]string{
		".replit":      "run = \"npm start\"\n",
		"package.json": `{"scripts":{"start":"node index.js"}}`,
		"index.js":     "const db = process.env.REPLIT_DB_URL; const k = process.env.STRIPE_KEY\n",
	}
	r, err := Inspect(context.Background(), Request{Repo: repoOf(t, files)})
	if err != nil {
		t.Fatal(err)
	}
	warn := "This app uses Replit's own services (REPLIT_DB_URL), which work only on Replit."
	if !reflect.DeepEqual(r.Missing, []string{"STRIPE_KEY"}) || !reflect.DeepEqual(r.Warnings, []string{warn}) || r.Builder != "Replit" {
		t.Fatalf("%+v", r)
	}
	got := Warnings(tree(t, files), nil, "production", "generate")
	if !reflect.DeepEqual(got, []string{"The code reads STRIPE_KEY, which is not set in production.", warn}) {
		t.Fatalf("build warnings %q", got)
	}
}

func TestExpoRefused(t *testing.T) {
	files := map[string]string{"package.json": `{"dependencies":{"expo":"~52.0.0","react-native":"0.76"},"scripts":{"start":"expo start"}}`}
	want := "This is an Expo app for phones. Yawl runs web apps and servers."
	if _, err := Inspect(context.Background(), Request{Repo: repoOf(t, files)}); err == nil || err.Error() != want {
		t.Fatalf("Inspect err = %v", err)
	}
	if _, err := Generate(tree(t, files), nil, spec.Steps{}); err == nil || err.Error() != want {
		t.Fatalf("Generate err = %v", err)
	}
}

func TestReplitShArrayKeepsQuoting(t *testing.T) {
	df := gen(t, map[string]string{"package.json": `{"scripts":{"build":"tsc"}}`,
		".replit": "[deployment]\nbuild = [\"sh\", \"-c\", \"npm run build\"]\nrun = [\"sh\", \"-c\", \"npm run start\"]\n"})
	mustContain(t, df, `RUN sh -c 'npm run build'`, `CMD ["sh","-c","sh -c 'npm run start'"]`)
}

func TestReplitTopLevelRunIsALastResort(t *testing.T) {
	top := "run = \"npm run dev\"\n"
	df := gen(t, map[string]string{"package.json": `{"scripts":{"dev":"tsx server","start":"node dist/index.js"}}`, ".replit": top})
	mustContain(t, df, `CMD ["sh","-c","npm start"]`)
	df = gen(t, map[string]string{"go.mod": "module a\n\ngo 1.23\n", ".replit": "run = \"go run .\"\n[deployment]\nrun = [\"sh\", \"-c\", \"go run main.go\"]\n"})
	mustContain(t, df, `CMD ["/app"]`)
}

func TestReplitInlineComment(t *testing.T) {
	run, build, port, deployed := replit(tree(t, map[string]string{".replit": "[deployment]\nrun = [\"npm\", \"start\"] # web\nbuild = \"npm run build\" # assets\n\n[[ports]]\nlocalPort = 5000 # app\n"}))
	if run != "npm start" || build != "npm run build" || port != 5000 || !deployed {
		t.Fatalf("run %q build %q port %d deployed %v", run, build, port, deployed)
	}
	run, _, _, _ = replit(tree(t, map[string]string{".replit": "[deployment]\nrun = \"a # b\" # c\n"}))
	if run != "a # b" {
		t.Fatalf("%q", run)
	}
}

func TestReplitMultiLine(t *testing.T) {
	run, build, port, deployed := replit(tree(t, map[string]string{".replit": "run = \"npm run dev\"\n\n[deployment]\nrun = [\n  \"node\",\n  \"dist/index.js\",\n]\nbuild = \"\"\"\nnpm run build\"\"\"\n\n[[ports]]\nlocalPort = 3000\nexternalPort = 80\n\n[[ports]]\nlocalPort = 5000\n"}))
	if run != "node dist/index.js" || build != "npm run build" || port != 3000 || !deployed {
		t.Fatalf("run %q build %q port %d deployed %v", run, build, port, deployed)
	}
}
