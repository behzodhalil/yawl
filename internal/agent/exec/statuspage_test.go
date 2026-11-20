package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestStatusPageFile(t *testing.T) {
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	d.StatusDir = filepath.Join(t.TempDir(), "status")
	run := func(a proto.StatusPageArgs) proto.Result {
		return d.Handle(context.Background(), cmd(t, proto.CmdStatusPage, a))
	}
	file := filepath.Join(d.StatusDir, "sp_abc.html")
	if res := run(proto.StatusPageArgs{ID: "sp_abc", HTML: "<p>one</p>"}); !res.OK {
		t.Fatalf("%+v", res)
	}
	run(proto.StatusPageArgs{ID: "sp_abc", HTML: "<p>two</p>"})
	if b, _ := os.ReadFile(file); string(b) != "<p>two</p>" {
		t.Fatalf("%q", b)
	}
	if res := run(proto.StatusPageArgs{ID: "sp_abc"}); !res.OK {
		t.Fatalf("remove: %+v", res)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("the file stayed")
	}
	if res := run(proto.StatusPageArgs{ID: "sp_abc"}); !res.OK {
		t.Fatalf("removing what is gone: %+v", res)
	}
	for _, id := range []string{"", "../etc/x", "a.b", "A", "a/b"} {
		if res := run(proto.StatusPageArgs{ID: id, HTML: "x"}); res.OK {
			t.Fatalf("%q was taken", id)
		}
	}
	if res := run(proto.StatusPageArgs{ID: "sp_big", HTML: strings.Repeat("x", maxStatusHTML+1)}); res.OK {
		t.Fatal("over 1 MB was taken")
	}
}

func TestEnsureDomainStatusPage(t *testing.T) {
	d, px := domainDispatcher(t, []string{"5.6.7.8"}, false)
	d.StatusDir = "/run/yawl/status"
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDomain, proto.EnsureDomainArgs{Host: "shop.acme.uz", ExpectIP: "5.6.7.8", Options: proto.RouteOptions{Status: "sp_abc"}}))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	if px.routes["shop.acme.uz"] != "" || px.opts["shop.acme.uz"].Status != "/run/yawl/status/sp_abc.html" {
		t.Fatalf("%v %+v", px.routes, px.opts)
	}
	res = d.Handle(context.Background(), cmd(t, proto.CmdEnsureDomain, proto.EnsureDomainArgs{Host: "shop.acme.uz", ExpectIP: "5.6.7.8", Options: proto.RouteOptions{Status: "../x"}}))
	if res.OK {
		t.Fatal("a path was taken as an id")
	}
}
