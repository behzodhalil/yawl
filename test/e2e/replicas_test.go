//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
)

func TestE2EReplicas(t *testing.T) {
	b := setup(t)
	ctx := context.Background()
	if _, err := b.store().DB.Exec(`UPDATE services SET replicas = 2 WHERE name = 'hello'`); err != nil {
		t.Fatal(err)
	}
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	rt, err := runtime.NewDocker()
	if err != nil {
		t.Fatal(err)
	}
	copies := func() []runtime.Container {
		cs, _ := rt.List(ctx, map[string]string{"yawl.project": "e2e", "yawl.service": "hello"})
		var live []runtime.Container
		for _, c := range cs {
			if st, err := rt.Inspect(ctx, c.ID); err == nil && st.Running && c.Labels["yawl.replica"] != "" {
				live = append(live, c)
			}
		}
		return live
	}
	first := copies()
	if len(first) != 2 || first[0].Labels["yawl.deploy"] != first[1].Labels["yawl.deploy"] {
		t.Fatalf("copies %+v", first)
	}
	for _, c := range first {
		if c.Labels["yawl.replica"] == "2" {
			rt.Stop(ctx, c.ID, time.Second)
		}
	}
	for i := 0; i < 20; i++ {
		if _, err := get(); err != nil {
			t.Fatalf("request %d with one copy stopped: %v", i, err)
		}
	}
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "two new copies, the old ones gone", func() bool {
		now := copies()
		return len(now) == 2 && now[0].Labels["yawl.deploy"] != first[0].Labels["yawl.deploy"] && now[0].Labels["yawl.deploy"] == now[1].Labels["yawl.deploy"]
	})
}
