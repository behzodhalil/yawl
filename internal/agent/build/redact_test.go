package build

import (
	"context"
	"strings"
	"testing"
)

func TestCloneErrorHidesCredentials(t *testing.T) {
	_, _, err := Build(context.Background(), nil, Request{Repo: "https://deploy:s3cret-token@127.0.0.1:1/acme/api.git", Branch: "main", Image: "yawl/x:1"})
	if err == nil {
		t.Fatal("clone of a closed port succeeded")
	}
	if strings.Contains(err.Error(), "s3cret-token") {
		t.Fatalf("error leaks the password: %v", err)
	}
}

func TestCloneErrorHidesTokenAsUser(t *testing.T) {
	_, _, err := Build(context.Background(), nil, Request{Repo: "https://ghp_abcdef123456@127.0.0.1:1/acme/api.git", Branch: "main", Image: "yawl/x:1"})
	if err == nil || strings.Contains(err.Error(), "ghp_abcdef123456") {
		t.Fatalf("error leaks the token: %v", err)
	}
}
