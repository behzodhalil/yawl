package exec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestPullCause(t *testing.T) {
	ref := "ghcr.io/acme/api:1"
	for msg, want := range map[string]string{
		"Error response from daemon: Head \"https://ghcr.io/v2/acme/api/manifests/1\": unauthorized": "ghcr.io refused the pull: add a login for ghcr.io in Integrations, Registries.",
		"pull access denied for acme/api, repository does not exist or may require 'docker login'":   "ghcr.io refused the pull: add a login for ghcr.io in Integrations, Registries.",
		"manifest unknown": "ghcr.io/acme/api:1 does not exist.",
		"Error response from daemon: ghcr.io/acme/api:1: not found":         "ghcr.io/acme/api:1 does not exist.",
		"no matching manifest for linux/arm64 in the manifest list entries": "ghcr.io/acme/api:1 has no linux/arm64 image; this server is arm64.",
		"dial tcp: i/o timeout": "pull ghcr.io/acme/api:1: dial tcp: i/o timeout",
	} {
		if got := pullCause(ref, errors.New(msg), "arm64"); got != want {
			t.Errorf("%s:\n got %q\nwant %q", msg, got, want)
		}
	}
}

func TestDispatchPull(t *testing.T) {
	rt := &runtime.Fake{Digest: "sha256:abc"}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdPull, proto.PullArgs{Ref: "ghcr.io/acme/api:1", Tag: "yawl/acme-production-api:dep_1",
		Auth: &proto.RegistryAuth{Username: "acme", Token: "ghp_secret"}}))
	var data proto.PullData
	json.Unmarshal(res.Data, &data)
	if !res.OK || data.Digest != "sha256:abc" || len(rt.Pulled) != 1 || rt.Pulled[0] != "ghcr.io/acme/api:1 yawl/acme-production-api:dep_1 acme ghp_secret" {
		t.Fatalf("%+v %v", res, rt.Pulled)
	}
	if strings.Contains(string(res.Data)+res.Cause, "ghp_secret") {
		t.Fatal("the token came back")
	}
}
