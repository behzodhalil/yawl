package conn

import (
	"errors"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
)

func TestAgentReadsChallengeAcrossVersions(t *testing.T) {
	b := []byte(`{"type":"challenge","id":"","version":2,"body":{"nonce":"MDEyMzQ1Njc4OWFiY2RlZg==","server_version":"1.2.0"}}`)
	ch, err := readChallenge(b)
	if !errors.Is(err, proto.ErrVersion) || ch.ServerVersion != "1.2.0" {
		t.Fatalf("%+v %v", ch, err)
	}
	b = []byte(`{"type":"challenge","id":"","version":1,"body":{"nonce":"MDEyMzQ1Njc4OWFiY2RlZg==","server_version":"1.2.0"}}`)
	if ch, err := readChallenge(b); err != nil || ch.ServerVersion != "1.2.0" || len(ch.Nonce) != 16 {
		t.Fatalf("%+v %v", ch, err)
	}
	if _, err := readChallenge([]byte(`{"type":"hello","version":1,"body":{}}`)); err == nil {
		t.Fatal("a hello was taken for a challenge")
	}
}
