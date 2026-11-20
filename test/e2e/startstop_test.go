//go:build e2e

package e2e

import (
	"testing"
	"time"
)

func TestE2EStartStop(t *testing.T) {
	b := setup(t)
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if v, err := get(); err != nil || v == "" {
		t.Fatalf("before: %q %v", v, err)
	}
	if out, err := run(t, "", "stop", "hello"); err != nil {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	waitFor(t, 30*time.Second, "hello off", func() bool { v, err := get(); return err != nil || v == "" })
	if out, err := run(t, "", "start", "hello"); err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	waitFor(t, 30*time.Second, "hello back", func() bool { v, err := get(); return err == nil && v != "" })
	if out, err := run(t, "", "restart", "hello"); err != nil {
		t.Fatalf("restart: %v\n%s", err, out)
	}
	if v, err := get(); err != nil || v == "" {
		t.Fatalf("after restart: %q %v", v, err)
	}
}
