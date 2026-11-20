//go:build docker

package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestExecShell(t *testing.T) {
	ctx := context.Background()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("yawl-test-exec-%d", time.Now().UnixNano())
	rt.EnsureNetwork(ctx, "yawl-test-net-once")
	id, err := rt.Start(ctx, ContainerSpec{Name: name, Image: "alpine:3", Network: "yawl-test-net-once", Cmd: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id) })
	sh, err := rt.Exec(ctx, name, []string{"sh"}, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	if err := sh.Resize(ctx, 120, 40); err != nil {
		t.Fatal(err)
	}
	sh.Write([]byte("echo yawl-$((40+2)); stty size; exit\n"))
	var out bytes.Buffer
	buf := make([]byte, 4096)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		n, err := sh.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(out.String(), "yawl-42") || !strings.Contains(out.String(), "40 120") {
		t.Fatalf("%q", out.String())
	}
}

func TestExecCloseEndsShell(t *testing.T) {
	ctx := context.Background()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("yawl-test-exec-close-%d", time.Now().UnixNano())
	rt.EnsureNetwork(ctx, "yawl-test-net-once")
	id, err := rt.Start(ctx, ContainerSpec{Name: name, Image: "alpine:3", Network: "yawl-test-net-once", Cmd: []string{"sleep", "120"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id) })
	sh, err := rt.Exec(ctx, name, []string{"sh"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	sh.Write([]byte("sleep 600\n"))
	time.Sleep(500 * time.Millisecond)
	execID := sh.(*dockerShell).id
	sh.Close()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		in, err := rt.C.ExecInspect(ctx, execID, client.ExecInspectOptions{})
		if err == nil && !in.Running && !sleeping600() {
			return
		}
	}
	t.Fatalf("the shell or what it started still runs after Close (sleep 600 alive: %v)", sleeping600())
}

func sleeping600() bool {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		if b, err := os.ReadFile("/proc/" + e.Name() + "/cmdline"); err == nil && string(b) == "sleep\x00600\x00" {
			return true
		}
	}
	return false
}
