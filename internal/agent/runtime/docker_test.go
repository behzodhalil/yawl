//go:build docker

package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestDockerStartStop(t *testing.T) {
	ctx := context.Background()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.EnsureNetwork(ctx, "yawl-test-net-rt"); err != nil {
		t.Fatal(err)
	}
	if err := rt.EnsureNetwork(ctx, "yawl-test-net-rt"); err != nil {
		t.Fatalf("EnsureNetwork is not idempotent: %v", err)
	}
	id, err := rt.Start(ctx, ContainerSpec{Name: fmt.Sprintf("yawl-test-whoami-%d", time.Now().UnixNano()), Image: "traefik/whoami", Network: "yawl-test-net-rt"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id) })
	st, err := rt.Inspect(ctx, id)
	if err != nil || !st.Running || !st.IP.IsValid() {
		t.Fatalf("not running with an IP: %+v %v", st, err)
	}
	// "running" is not "listening": under load (other packages build in
	// parallel) whoami binds a moment later, so poll as the health check does.
	var ls []Listen
	found := false
	for deadline := time.Now().Add(10 * time.Second); !found && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if ls, err = rt.Listening(ctx, id); err != nil {
			t.Fatalf("Listening: %v", err)
		}
		for _, l := range ls {
			found = found || (l.Port == 80 && !l.Loopback())
		}
	}
	if !found {
		t.Fatalf("whoami should listen on :80, read %v", ls)
	}
	if err := rt.Stop(ctx, id, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if st, _ := rt.Inspect(ctx, id); st.Running {
		t.Fatal("still running after Stop")
	}
}

func TestDockerLogs(t *testing.T) {
	ctx := context.Background()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	rt.EnsureNetwork(ctx, "yawl-test-net-rt")
	label := fmt.Sprintf("logs-%d", time.Now().UnixNano())
	id, err := rt.Start(ctx, ContainerSpec{Name: "yawl-test-" + label, Image: "alpine:3", Network: "yawl-test-net-rt",
		Labels: map[string]string{"yawl.test": label}, Cmd: []string{"sh", "-c", "echo one; sleep 1; echo two; echo three >&2; sleep 60"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Remove(ctx, id) })
	var all []LogLine
	for deadline := time.Now().Add(15 * time.Second); len(all) < 3 && time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		all = nil
		if err := rt.Logs(ctx, id, time.Time{}, func(l LogLine) { all = append(all, l) }); err != nil {
			t.Fatal(err)
		}
	}
	streams := map[string]string{} // ROADMAP 35; Docker may keep "two" and "three" in either order, its copier reads each stream apart
	for _, l := range all {
		streams[l.Text] = l.Stream
	}
	if len(all) != 3 || all[0].Text != "one" || streams["one"] != "" || streams["two"] != "" || streams["three"] != "stderr" {
		t.Fatalf("lines %+v", all)
	}
	var after []string
	rt.Logs(ctx, id, all[0].Time, func(l LogLine) { after = append(after, l.Text) })
	if len(after) != 2 {
		t.Fatalf("since the first: %v", after)
	}
	cs, err := rt.List(ctx, map[string]string{"yawl.test": label})
	if err != nil || len(cs) != 1 || cs[0].ID != id {
		t.Fatalf("list %+v %v", cs, err)
	}
}

func TestDockerPruneByTag(t *testing.T) {
	ctx := context.Background()
	rt, err := NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.pullIfMissing(ctx, "alpine:3"); err != nil {
		t.Fatal(err)
	}
	repo := fmt.Sprintf("yawl/test-prune-%d", time.Now().UnixNano())
	for _, tag := range []string{"a", "b", "c"} {
		if _, err := rt.C.ImageTag(ctx, client.ImageTagOptions{Source: "alpine:3", Target: repo + ":" + tag}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { rt.PruneImages(ctx, repo, nil) })
	if err := rt.PruneImages(ctx, repo, []string{repo + ":a", repo + ":c"}); err != nil {
		t.Fatal(err)
	}
	for tag, want := range map[string]bool{"a": true, "b": false, "c": true} {
		_, err := rt.C.ImageInspect(ctx, repo+":"+tag)
		if (err == nil) != want {
			t.Fatalf("%s: present=%v, want %v", tag, err == nil, want)
		}
	}
	if _, err := rt.C.ImageInspect(ctx, "alpine:3"); err != nil {
		t.Fatal("pruning untagged a shared image's other name")
	}
}
