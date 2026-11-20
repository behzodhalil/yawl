//go:build docker

package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestRunOnceStreams(t *testing.T) {
	ctx := context.Background()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	rt.EnsureNetwork(ctx, "yawl-test-net-once")
	name := fmt.Sprintf("yawl-test-once-%d", time.Now().UnixNano())
	var out bytes.Buffer
	exit, stderr, err := rt.RunOnce(ctx, ContainerSpec{Name: name, Image: "alpine:3", Network: "yawl-test-net-once",
		Cmd: []string{"sh", "-c", "cat; echo err >&2; exit 3"}}, strings.NewReader("hello"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello" || exit != 3 || stderr != "err\n" {
		t.Fatalf("stdout %q exit %d stderr %q", out.String(), exit, stderr)
	}
	if _, err := rt.C.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); err == nil {
		t.Fatal("the one-off container was left behind")
	}
}

func TestAliasResolves(t *testing.T) {
	ctx := context.Background()
	rt, _ := NewDocker()
	rt.EnsureNetwork(ctx, "yawl-test-net-alias")
	id, err := rt.Start(ctx, ContainerSpec{Name: fmt.Sprintf("yawl-test-who-%d", time.Now().UnixNano()), Image: "traefik/whoami",
		Network: "yawl-test-net-alias", Aliases: []string{"who"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id) })
	var exit int
	var stderr string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if exit, stderr, err = rt.RunOnce(ctx, ContainerSpec{Image: "alpine:3", Network: "yawl-test-net-alias",
			Cmd: []string{"wget", "-qO-", "http://who/"}}, nil, &bytes.Buffer{}); err == nil && exit == 0 {
			return
		}
	}
	t.Fatalf("wget http://who/ exit %d: %s %v", exit, stderr, err)
}

func TestRunOnceNotLogged(t *testing.T) {
	ctx := context.Background()
	rt, _ := NewDocker()
	rt.EnsureNetwork(ctx, "yawl-test-net-once")
	name := fmt.Sprintf("yawl-test-nolog-%d", time.Now().UnixNano())
	done := make(chan struct{})
	go func() {
		defer close(done)
		rt.RunOnce(ctx, ContainerSpec{Name: name, Image: "alpine:3", Network: "yawl-test-net-once",
			Cmd: []string{"sh", "-c", "echo plaintext-row; sleep 3"}}, nil, &bytes.Buffer{})
	}()
	var logType string
	for deadline := time.Now().Add(20 * time.Second); logType == "" && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if res, err := rt.C.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); err == nil && res.Container.HostConfig != nil {
			logType = res.Container.HostConfig.LogConfig.Type
		}
	}
	<-done
	if logType != "none" {
		t.Fatalf("one-off container log driver %q, want none", logType)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("upload stopped") }

func TestRunOnceWriterFails(t *testing.T) {
	ctx := context.Background()
	rt, _ := NewDocker()
	rt.EnsureNetwork(ctx, "yawl-test-net-once")
	name := fmt.Sprintf("yawl-test-stuck-%d", time.Now().UnixNano())
	errc := make(chan error, 1)
	go func() {
		_, _, err := rt.RunOnce(ctx, ContainerSpec{Name: name, Image: "alpine:3", Network: "yawl-test-net-once", Cmd: []string{"yes"}}, nil, failWriter{})
		errc <- err
	}()
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "upload stopped") {
			t.Fatalf("err = %v, want the writer's error", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("RunOnce did not return after its stdout writer failed")
	}
	if _, err := rt.C.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); err == nil {
		t.Fatal("the container was left running")
	}
}

type cutReader struct{}

func (cutReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestRunOnceStdinCut(t *testing.T) {
	ctx := context.Background()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	rt.EnsureNetwork(ctx, "yawl-test-net-once")
	var out bytes.Buffer
	_, _, err = rt.RunOnce(ctx, ContainerSpec{Name: fmt.Sprintf("yawl-test-cut-%d", time.Now().UnixNano()), Image: "alpine:3", Network: "yawl-test-net-once",
		Cmd: []string{"sh", "-c", "cat >/dev/null; echo finished"}}, io.MultiReader(strings.NewReader("abc"), cutReader{}), &out)
	if err == nil || strings.Contains(out.String(), "finished") {
		t.Fatalf("err %v out %q", err, out.String())
	}
}

type lateWriter struct {
	t    *testing.T
	mu   sync.Mutex
	done bool
}

func (w *lateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		w.t.Error("stdout written after RunOnce returned")
	}
	return len(p), nil
}

func TestRunOnceTimeoutStopsCopy(t *testing.T) {
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	rt.EnsureNetwork(context.Background(), "yawl-test-net-once")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	w := &lateWriter{t: t}
	_, _, err = rt.RunOnce(ctx, ContainerSpec{Name: fmt.Sprintf("yawl-test-timeout-%d", time.Now().UnixNano()), Image: "alpine:3", Network: "yawl-test-net-once",
		Cmd: []string{"yes"}}, nil, w)
	w.mu.Lock()
	w.done = true
	w.mu.Unlock()
	if err == nil {
		t.Fatal("a run past its deadline returned no error")
	}
	time.Sleep(500 * time.Millisecond)
}
