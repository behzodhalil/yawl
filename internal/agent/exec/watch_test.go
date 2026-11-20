package exec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/metrics"
	"github.com/behzodhalil/yawl/internal/proto"
)

func TestWatchCommandWritesFile(t *testing.T) {
	d := &Dispatcher{WatchFile: filepath.Join(t.TempDir(), "watch.json")}
	key := strings.Repeat("ab", 32)
	for _, u := range []string{"https://w1.example.workers.dev", "https://w2.example.workers.dev"} {
		c, _ := proto.NewCommand("k", proto.CmdWatch, proto.WatchArgs{URL: u, Key: key})
		if res := d.Handle(context.Background(), c); !res.OK {
			t.Fatalf("%+v", res)
		}
		b, err := os.ReadFile(d.WatchFile)
		if err != nil {
			t.Fatal(err)
		}
		var got proto.WatchArgs
		json.Unmarshal(b, &got)
		if got.URL != u || got.Key != key {
			t.Fatalf("file %s", b)
		}
		if fi, _ := os.Stat(d.WatchFile); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode())
		}
	}
}

func TestWatchCommandRefusesBadArgs(t *testing.T) {
	d := &Dispatcher{WatchFile: filepath.Join(t.TempDir(), "watch.json")}
	for _, a := range []proto.WatchArgs{{URL: "https://w", Key: "short"}, {URL: "ftp://w", Key: strings.Repeat("ab", 32)}} {
		c, _ := proto.NewCommand("k", proto.CmdWatch, a)
		if res := d.Handle(context.Background(), c); res.OK {
			t.Fatalf("took %+v", a)
		}
	}
	if _, err := os.Stat(d.WatchFile); err == nil {
		t.Fatal("wrote a file for bad arguments")
	}
}

func TestMetricsCommand(t *testing.T) {
	d := &Dispatcher{Ring: &metrics.Ring{N: 60}}
	for _, at := range []int64{60_000, 120_000, 180_000} {
		d.Ring.Add(proto.Sample{At: at})
	}
	c, _ := proto.NewCommand("m", proto.CmdMetrics, proto.MetricsArgs{Since: 60_000})
	res := d.Handle(context.Background(), c)
	var data proto.MetricsData
	json.Unmarshal(res.Data, &data)
	if !res.OK || len(data.Samples) != 2 || data.Samples[0].At != 120_000 {
		t.Fatalf("%+v %+v", res, data)
	}
	if !proto.Unrecorded(proto.CmdMetrics) {
		t.Fatal("Metrics must be unrecorded")
	}
}
