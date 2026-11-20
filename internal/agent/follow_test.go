package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/update"
)

func releaseServer(t *testing.T, version string, good bool) (*httptest.Server, ed25519.PublicKey, *int) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	bin := []byte("agent " + version)
	sum := sha256.Sum256(bin)
	name := "yawl-" + goruntime.GOOS + "-" + goruntime.GOARCH
	sums := []byte(fmt.Sprintf("# yawl %s\n%s  %s\n", version, hex.EncodeToString(sum[:]), name))
	sig := ed25519.Sign(priv, sums)
	if !good {
		sig[0] ^= 1
	}
	fetches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v" + version + "/SHA256SUMS":
			fetches++
			w.Write(sums)
		case "/v" + version + "/SHA256SUMS.sig":
			w.Write(sig)
		case "/v" + version + "/" + name:
			w.Write(bin)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, pub, &fetches
}

func follower(t *testing.T, base string, key ed25519.PublicKey, current string) (*Follow, string, *int) {
	exe := filepath.Join(t.TempDir(), "yawl")
	os.WriteFile(exe, []byte("agent "+current), 0o755)
	exits := 0
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	return &Follow{Updater: func() (update.Updater, error) { return update.Updater{Base: base, Key: key}, nil },
		Current: current, Exe: exe, Exit: func() { exits++ }, Every: time.Hour, Now: func() time.Time { return now }}, exe, &exits
}

func TestAgentFollowsOnce(t *testing.T) {
	ctx := context.Background()
	srv, key, _ := releaseServer(t, "1.2.0", true)
	f, exe, exits := follower(t, srv.URL, key, "1.1.0")
	f.Check(ctx, "1.2.0")
	if b, _ := os.ReadFile(exe); string(b) != "agent 1.2.0" || *exits != 1 {
		t.Fatalf("%q exits %d", b, *exits)
	}

	bad, key2, fetches := releaseServer(t, "1.3.0", false)
	f, exe, exits = follower(t, bad.URL, key2, "1.1.0")
	f.Check(ctx, "1.3.0")
	f.Check(ctx, "1.3.0")
	if b, _ := os.ReadFile(exe); string(b) != "agent 1.1.0" || *exits != 0 || *fetches != 1 {
		t.Fatalf("%q exits %d fetches %d", b, *exits, *fetches)
	}

	for _, c := range [][2]string{{"dev", "1.1.0"}, {"1.2.0", "dev"}, {"1.1.0", "1.1.0"}, {"", "1.1.0"}} {
		f, _, exits := follower(t, srv.URL, key, c[1])
		f.Check(ctx, c[0])
		if *exits != 0 {
			t.Fatalf("server %q agent %q exited", c[0], c[1])
		}
	}
}

func TestFollowRemembersAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	bad, key, fetches := releaseServer(t, "1.3.0", false)
	state := filepath.Join(t.TempDir(), "update-attempt")
	f, _, _ := follower(t, bad.URL, key, "1.1.0")
	f.StatePath = state
	f.Check(ctx, "1.3.0")
	again, _, _ := follower(t, bad.URL, key, "1.1.0")
	again.StatePath = state
	again.Check(ctx, "1.3.0")
	if *fetches != 1 {
		t.Fatalf("fetched %d times across a restart", *fetches)
	}
}

func TestFollowWaitsForCommands(t *testing.T) {
	ctx := context.Background()
	srv, key, _ := releaseServer(t, "1.2.0", true)
	f, _, exits := follower(t, srv.URL, key, "1.1.0")
	release := make(chan struct{})
	var left time.Duration
	f.Wait = func(c context.Context) error {
		d, _ := c.Deadline()
		left = time.Until(d)
		<-release
		if *exits != 0 {
			t.Error("exited before the commands finished")
		}
		return nil
	}
	go func() { time.Sleep(50 * time.Millisecond); close(release) }()
	f.Check(ctx, "1.2.0")
	if *exits != 1 || left > 30*time.Second || left < 29*time.Second {
		t.Fatalf("exits %d, waited up to %s", *exits, left)
	}
}
