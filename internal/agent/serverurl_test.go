package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestServerURL(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "server_url"), []byte("https://panel.1-2-3-4.sslip.io"), 0o600)
	if err := Run(context.Background(), []string{"server-url", "https://panel.acme.uz/", "--data", dir}, "dev"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "server_url")); string(b) != "https://panel.acme.uz" {
		t.Fatalf("%q", b)
	}
	if err := Run(context.Background(), []string{"server-url", "panel.acme.uz", "--data", dir}, "dev"); err == nil {
		t.Fatal("a URL without a scheme was accepted")
	}
	if err := Run(context.Background(), []string{"server-url", "https://x", "--data", t.TempDir()}, "dev"); err == nil {
		t.Fatal("an agent that never joined was given a URL")
	}
}
