//go:build docker

package build

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func gitRepo(t *testing.T, app string, over map[string]string) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	src := "../../../testdata/apps/" + app
	entries, _ := os.ReadDir(src)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644)
	}
	for name, body := range over {
		if body == "" {
			os.Remove(filepath.Join(dir, name))
			continue
		}
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := r.Worktree()
	w.AddGlob(".")
	h, err := w.Commit("v1", &git.CommitOptions{All: true, Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	return dir, h.String()
}

func docker(t *testing.T) (*client.Client, context.Context) {
	c, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	return c, ctx
}

func TestBuildFromGit(t *testing.T) {
	dir, sha := gitRepo(t, "hello", nil)
	c, ctx := docker(t)
	got, _, err := Build(ctx, c, Request{Repo: "file://" + dir, Image: "yawl/test-hello:t1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != sha {
		t.Fatalf("built %s, want head %s", got.Commit, sha)
	}
	img, err := c.ImageInspect(ctx, "yawl/test-hello:t1")
	if err != nil {
		t.Fatalf("image not tagged: %v", err)
	}
	if img.Config.Labels["yawl.commit"] != sha {
		t.Fatalf("label yawl.commit = %q", img.Config.Labels["yawl.commit"])
	}
}

func TestBuildSecretNotInImage(t *testing.T) {
	const value = "s3cr3t-build-value"
	dir, _ := gitRepo(t, "secret", nil)
	c, ctx := docker(t)
	if _, _, err := Build(ctx, c, Request{Repo: "file://" + dir, Image: "yawl/test-secret:t1",
		Secrets: map[string][]byte{"TOKEN": []byte(value)}}); err != nil {
		t.Fatal(err)
	}
	hist, err := c.ImageHistory(ctx, "yawl/test-secret:t1")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hist.Items {
		if strings.Contains(h.CreatedBy, value) {
			t.Fatalf("history carries the secret: %s", h.CreatedBy)
		}
	}
	rc, err := c.ImageSave(ctx, []string{"yawl/test-secret:t1"})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	all, _ := io.ReadAll(rc)
	if bytes.Contains(all, []byte(value)) {
		t.Fatal("the saved image carries the secret")
	}
}

func TestBuildFailureTail(t *testing.T) {
	dir, _ := gitRepo(t, "hello", map[string]string{"Dockerfile": "FROM alpine:3\nRUN echo boom-line && false\n"})
	c, ctx := docker(t)
	_, _, err := Build(ctx, c, Request{Repo: "file://" + dir, Image: "yawl/test-fail:t1"})
	if err == nil || !strings.Contains(err.Error(), "boom-line") {
		t.Fatalf("cause = %v, want the failing step's output", err)
	}
}

func TestBuildDetectedGo(t *testing.T) {
	dir, _ := gitRepo(t, "hello-go", nil)
	c, ctx := docker(t)
	if _, _, err := Build(ctx, c, Request{Repo: "file://" + dir, Image: "yawl/test-detected:t1"}); err != nil {
		t.Fatal(err)
	}
	img, err := c.ImageInspect(ctx, "yawl/test-detected:t1")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(img.Config.Cmd, " "); got != "/app" {
		t.Fatalf("CMD = %q, want /app", got)
	}
}

func TestBuildStaticSite(t *testing.T) {
	dir, _ := gitRepo(t, "hello-vite", nil)
	c, ctx := docker(t)
	if _, _, err := Build(ctx, c, Request{Repo: "file://" + dir, Image: "yawl/test-static:t1"}); err != nil {
		t.Fatal(err)
	}
	const port = "18734"
	res, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     &container.Config{Image: "yawl/test-static:t1", Env: []string{"PORT=" + port}},
		HostConfig: &container.HostConfig{NetworkMode: "host"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.ContainerRemove(context.WithoutCancel(ctx), res.ID, client.ContainerRemoveOptions{Force: true})
	if _, err := c.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	get := func(path string) (int, string) {
		for i := 0; ; i++ {
			r, err := http.Get("http://127.0.0.1:" + port + path)
			if err == nil {
				defer r.Body.Close()
				b, _ := io.ReadAll(r.Body)
				return r.StatusCode, string(b)
			}
			if i == 50 {
				t.Fatalf("GET %s: %v", path, err)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	for _, p := range []string{"/", "/dashboard/settings"} {
		if code, body := get(p); code != 200 || !strings.Contains(body, "hello-vite index") {
			t.Fatalf("GET %s = %d %q", p, code, body)
		}
	}
	if code, body := get("/assets/app.js"); code != 200 || !strings.Contains(body, "hello-vite app") {
		t.Fatalf("GET /assets/app.js = %d %q", code, body)
	}
}

func TestBuildRailpackPHP(t *testing.T) {
	dir, _ := gitRepo(t, "hello-php", nil)
	c, _ := docker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if _, _, err := Build(ctx, c, Request{Repo: "file://" + dir, Image: "yawl/test-php:t1", Secrets: map[string][]byte{"APP_KEY": []byte("k1")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ImageInspect(ctx, "yawl/test-php:t1"); err != nil {
		t.Fatalf("image %v", err)
	}
	const port = "18735"
	res, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     &container.Config{Image: "yawl/test-php:t1", Env: []string{"PORT=" + port}},
		HostConfig: &container.HostConfig{NetworkMode: "host"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.ContainerRemove(context.WithoutCancel(ctx), res.ID, client.ContainerRemoveOptions{Force: true})
	if _, err := c.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		r, err := http.Get("http://127.0.0.1:" + port + "/")
		if err == nil {
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if !strings.HasPrefix(string(b), "hello from php 8") {
				t.Fatalf("GET / = %d %q", r.StatusCode, b)
			}
			return
		}
		if i == 150 {
			t.Fatalf("GET /: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestBuildRoot(t *testing.T) {
	dir, _ := gitRepo(t, "hello", map[string]string{"Dockerfile": "FROM scratch\nLABEL built=root\n"})
	os.MkdirAll(filepath.Join(dir, "site"), 0o755)
	os.WriteFile(filepath.Join(dir, "site", "Dockerfile"), []byte("FROM busybox:1.36\nCOPY index.html /www/\nLABEL built=site\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "site", "index.html"), []byte("hi"), 0o644)
	r, _ := git.PlainOpen(dir)
	w, _ := r.Worktree()
	w.AddGlob(".")
	if _, err := w.Commit("site", &git.CommitOptions{All: true, Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	c, ctx := docker(t)
	if _, _, err := Build(ctx, c, Request{Repo: "file://" + dir, Image: "yawl/test-root:t1", Root: "site"}); err != nil {
		t.Fatal(err)
	}
	img, err := c.ImageInspect(ctx, "yawl/test-root:t1")
	if err != nil || img.Config.Labels["built"] != "site" {
		t.Fatalf("built from the wrong folder: %v %v", err, img.Config.Labels)
	}
	if _, _, err := Build(ctx, c, Request{Repo: "file://" + dir, Image: "yawl/test-root:t2", Root: "nope"}); err == nil || !strings.Contains(err.Error(), "root nope is not in the repository") {
		t.Fatalf("missing root: %v", err)
	}
}
