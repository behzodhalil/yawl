package build

import (
	"context"
	"fmt"
	iofs "io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

const sec = "npm-secret-value-123"

func logOf(chunks ...string) *buildLog {
	l := &buildLog{}
	for _, c := range chunks {
		l.Write([]byte(c))
	}
	return l
}

func TestTailMasksSecretAcrossChunks(t *testing.T) {
	l := logOf("step 1\nusing token npm-secret-", "value-123 now\n")
	got := l.cause(fmt.Errorf("exit 1"), Request{Secrets: map[string][]byte{"NPM_TOKEN": []byte(sec)}})
	if strings.Contains(got, "npm-secret") || !strings.Contains(got, "using token •••• now") {
		t.Fatalf("cause %q", got)
	}
}

func TestTailKeepsLastLinesMasked(t *testing.T) {
	var b strings.Builder
	for i := range 200 {
		fmt.Fprintf(&b, "webpack compiling module %03d with a fairly long line of ordinary output %s\n", i, sec)
	}
	b.WriteString("ERROR in ./src/index.ts: the real error\n")
	got := logOf(b.String()).cause(fmt.Errorf("exit 1"), Request{Token: "ghs_tok_abcdef", Secrets: map[string][]byte{"K": []byte(sec)}})
	if !strings.HasPrefix(got, "build failed: exit 1\n") || !strings.HasSuffix(got, "ERROR in ./src/index.ts: the real error") {
		t.Fatalf("cause lost its head or the error line:\n%s", got)
	}
	if len(got) > tailBytes || strings.Contains(got, "npm-secret") || strings.Contains(got, "123 \n") {
		t.Fatalf("cause is %d bytes or carries a fragment:\n%s", len(got), got)
	}
}

func TestTailBounded(t *testing.T) {
	l := logOf(strings.Repeat("x", logBufBytes) + sec + "\nlast line\n")
	if len(l.buf) > logBufBytes {
		t.Fatalf("buffer grew to %d", len(l.buf))
	}
	got := l.cause(fmt.Errorf("exit 1"), Request{})
	if !strings.HasSuffix(got, "last line") || strings.Contains(got, "xxx") {
		t.Fatalf("cause %q", got)
	}
}

func TestContextLeavesGitOut(t *testing.T) {
	dir := tree(t, map[string]string{".git/HEAD": "ref", ".gitignore": "x", ".dockerignore": "tmp", "src/.git": "sub", "index.js": "x"})
	paths := func(generated bool) []string {
		fs, err := contextFS(dir, generated)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		fs.Walk(context.Background(), "", func(p string, _ iofs.DirEntry, err error) error {
			out = append(out, p)
			return err
		})
		return out
	}
	if got := strings.Join(paths(true), " "); got != ".dockerignore .gitignore index.js src src/.git" {
		t.Fatalf("generated context %q", got)
	}
	if got := paths(false); !slices.Contains(got, ".git/HEAD") {
		t.Fatalf("own Dockerfile context %q", got)
	}
}

func TestConnectSaysDocker28(t *testing.T) {
	c, err := client.New(client.WithHost("unix://" + filepath.Join(t.TempDir(), "none.sock")))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := connect(ctx, c); err == nil || !strings.HasSuffix(err.Error(), "Yawl needs Docker 28 or newer.") {
		t.Fatalf("err = %v", err)
	}
}
