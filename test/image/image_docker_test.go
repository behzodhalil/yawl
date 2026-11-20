//go:build docker

package image

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/moby/moby/client"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

const dockerfile = "deploy/yawl.Dockerfile"

func dockerignore(t *testing.T, root string) *patternmatcher.PatternMatcher {
	f, err := os.Open(filepath.Join(root, dockerfile+".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	patterns, err := ignorefile.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	pm, err := patternmatcher.New(patterns)
	if err != nil {
		t.Fatal(err)
	}
	return pm
}

func context_(t *testing.T, root string) io.Reader {
	ignored := dockerignore(t, root)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		skip, err := ignored.MatchesOrParentMatches(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if skip {
			if d.IsDir() && !ignored.Exclusions() {
				return filepath.SkipDir
			}
			return nil
		}
		info, _ := d.Info()
		if !info.Mode().IsRegular() && !d.IsDir() {
			return nil
		}
		h, _ := tar.FileInfoHeader(info, "")
		h.Name = filepath.ToSlash(rel)
		tw.WriteHeader(h)
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			tw.Write(b)
		}
		return nil
	})
	tw.Close()
	return &buf
}

func TestYawlImage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.C.ImageBuild(ctx, context_(t, "../.."), client.ImageBuildOptions{Tags: []string{"yawl-test:image"}, Dockerfile: dockerfile, Remove: true})
	if err != nil {
		t.Fatal(err)
	}
	var last string
	dec := json.NewDecoder(res.Body)
	for {
		var m struct{ Stream, Error string }
		if dec.Decode(&m) != nil {
			break
		}
		if m.Error != "" {
			t.Fatalf("build: %s\n%s", m.Error, last)
		}
		last = m.Stream
	}
	res.Body.Close()
	t.Cleanup(func() {
		rt.C.ImageRemove(context.Background(), "yawl-test:image", client.ImageRemoveOptions{Force: true})
	})
	id, err := rt.Start(ctx, runtime.ContainerSpec{Name: "yawl-test-cell", Image: "yawl-test:image", HostNetwork: true,
		Env: []string{"YAWL_LISTEN=127.0.0.1:17000", "YAWL_PUBLIC_URL=http://127.0.0.1:17000", "YAWL_MANAGED=1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Stop(context.Background(), id, time.Second); rt.Remove(context.Background(), id) })
	var body []byte
	for i := 0; i < 60; i++ {
		if r, err := http.Get("http://127.0.0.1:17000/api/session"); err == nil {
			body, _ = io.ReadAll(r.Body)
			r.Body.Close()
			if r.StatusCode == 200 {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(string(body), `"managed":true`) {
		tail, _ := rt.Tail(ctx, id, 20)
		t.Fatalf("session %q\n%s", body, tail)
	}
}
