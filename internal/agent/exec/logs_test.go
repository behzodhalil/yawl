package exec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

var t0 = time.Date(2026, 10, 24, 3, 0, 0, 0, time.UTC)

func logsRuntime() *runtime.Fake {
	rt := &runtime.Fake{LogLines: map[string][]runtime.LogLine{}}
	labels := func(dep string) map[string]string {
		return map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "api", "yawl.deploy": dep}
	}
	rt.Start(context.Background(), runtime.ContainerSpec{Name: "old", Labels: labels("dep_1")})
	rt.Start(context.Background(), runtime.ContainerSpec{Name: "new", Labels: labels("dep_2")})
	rt.Start(context.Background(), runtime.ContainerSpec{Name: "other", Labels: map[string]string{"yawl.project": "acme", "yawl.env": "production", "yawl.service": "web"}})
	rt.LogLines["c1"] = []runtime.LogLine{{Time: t0, Text: "starting"}, {Time: t0.Add(2 * time.Second), Text: "error: x", Stream: "stderr"}}
	rt.LogLines["c2"] = []runtime.LogLine{{Time: t0.Add(time.Second), Text: "ok"}, {Time: t0.Add(3 * time.Second), Text: "ERROR again"}}
	rt.LogLines["c3"] = []runtime.LogLine{{Time: t0, Text: "not mine"}}
	return rt
}

func logs(t *testing.T, d *Dispatcher, a proto.LogsArgs) []proto.LogsLine {
	t.Helper()
	a.Project, a.Env, a.Service = "acme", "production", "api"
	res := d.Handle(context.Background(), cmd(t, proto.CmdLogs, a))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	var data proto.LogsData
	json.Unmarshal(res.Data, &data)
	return data.Lines
}

func texts(ls []proto.LogsLine) string {
	var out []string
	for _, l := range ls {
		out = append(out, l.Deploy+":"+l.Text)
	}
	return strings.Join(out, "|")
}

func TestLogsAcrossContainers(t *testing.T) {
	d := NewDispatcher(logsRuntime(), &fakeProxy{}, &fakeBuilder{})
	if got := texts(logs(t, d, proto.LogsArgs{Limit: 3})); got != "dep_2:ok|dep_1:error: x|dep_2:ERROR again" {
		t.Fatalf("got %s", got)
	}
	if got := texts(logs(t, d, proto.LogsArgs{Since: t0.Add(time.Second)})); got != "dep_1:error: x|dep_2:ERROR again" {
		t.Fatalf("since: got %s", got)
	}
}

func TestLogsStream(t *testing.T) {
	d := NewDispatcher(logsRuntime(), &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdLogs, proto.LogsArgs{Project: "acme", Env: "production", Service: "api", Limit: 2}))
	if !strings.Contains(string(res.Data), `"Text":"error: x","Stream":"stderr"}`) || !strings.Contains(string(res.Data), `"Text":"ERROR again"}`) {
		t.Fatalf("%s", res.Data)
	}
}

func TestLogsQuery(t *testing.T) {
	d := NewDispatcher(logsRuntime(), &fakeProxy{}, &fakeBuilder{})
	if got := texts(logs(t, d, proto.LogsArgs{Query: "ERROR"})); got != "dep_1:error: x|dep_2:ERROR again" {
		t.Fatalf("got %s", got)
	}
}

func TestLogsMasked(t *testing.T) {
	const secret = "fresh-secret-value-42"
	rt := logsRuntime()
	rt.LogLines["c2"] = append(rt.LogLines["c2"], runtime.LogLine{Time: t0.Add(4 * time.Second), Text: "key=" + secret})
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	got := texts(logs(t, d, proto.LogsArgs{Vars: []proto.SealedVar{{Key: "K", Box: sealTo(secret)}}}))
	if strings.Contains(got, secret) || !strings.Contains(got, "key=••••") {
		t.Fatalf("got %s", got)
	}
}

func TestLogsQueryCannotProbeSecrets(t *testing.T) {
	const secret = "s3cret-password-xyz"
	rt := logsRuntime()
	rt.LogLines["c2"] = append(rt.LogLines["c2"], runtime.LogLine{Time: t0.Add(4 * time.Second), Text: "url=postgres://app:" + secret + "@db"})
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	vars := []proto.SealedVar{{Key: "K", Box: sealTo(secret)}}
	if got := logs(t, d, proto.LogsArgs{Query: "app:s3c", Vars: vars}); len(got) != 0 {
		t.Fatalf("a guess at the secret matched: %s", texts(got))
	}
	if got := logs(t, d, proto.LogsArgs{Query: "url=", Vars: vars}); len(got) != 1 || strings.Contains(got[0].Text, secret) {
		t.Fatalf("got %s", texts(got))
	}
}

func TestLogsSinceOldestFirst(t *testing.T) {
	d := NewDispatcher(logsRuntime(), &fakeProxy{}, &fakeBuilder{})
	if got := texts(logs(t, d, proto.LogsArgs{Since: t0.Add(-time.Second), Limit: 2})); got != "dep_1:starting|dep_2:ok" {
		t.Fatalf("got %s", got)
	}
}

func TestLogsMaskedWithContainerEnv(t *testing.T) {
	const old = "rotated-away-secret-1"
	rt := logsRuntime()
	rt.Set("c1", runtime.State{Labels: rt.Started[0].Labels, Env: []string{"API_KEY=" + old, "PORT=10000"}})
	rt.LogLines["c1"] = append(rt.LogLines["c1"], runtime.LogLine{Time: t0.Add(5 * time.Second), Text: "key=" + old + " port=10000"})
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	got := texts(logs(t, d, proto.LogsArgs{}))
	if strings.Contains(got, old) || !strings.Contains(got, "key=•••• port=10000") {
		t.Fatalf("got %s", got)
	}
}

func TestLogsStderrOnly(t *testing.T) {
	d := NewDispatcher(logsRuntime(), &fakeProxy{}, &fakeBuilder{})
	if got := texts(logs(t, d, proto.LogsArgs{Stream: "stderr", Limit: 1})); got != "dep_1:error: x" {
		t.Fatalf("got %s", got)
	}
}
