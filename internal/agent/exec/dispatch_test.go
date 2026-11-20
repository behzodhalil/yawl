package exec

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/behzodhalil/yawl/internal/agent/proxy"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/agent/secrets"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

type fakeProxy struct {
	routes    map[string]string
	opts      map[string]proxy.Options
	redirects []string
	removed   []string
}

func (p *fakeProxy) RemoveRoute(_ context.Context, h string) error {
	p.removed = append(p.removed, h)
	delete(p.routes, h)
	return nil
}
func (p *fakeProxy) SetRedirect(_ context.Context, h string) error {
	p.redirects = append(p.redirects, h)
	return nil
}

func (p *fakeProxy) Ensure(context.Context) error { return nil }
func (p *fakeProxy) SetRoute(_ context.Context, h string, u []string, o proxy.Options) error {
	if p.routes == nil {
		p.routes, p.opts = map[string]string{}, map[string]proxy.Options{}
	}
	p.routes[h], p.opts[h] = strings.Join(u, ","), o
	return nil
}
func (p *fakeProxy) Routes(context.Context) (map[string]string, error) { return p.routes, nil }

type fakeBuilder struct {
	got     []string
	secrets map[string][]byte
	err     error
	subject string
}

func (b *fakeBuilder) Inspect(_ context.Context, a proto.InspectArgs) (proto.InspectData, error) {
	b.got = []string{a.Repo, a.Branch, a.Token}
	if b.err != nil {
		return proto.InspectData{}, b.err
	}
	return proto.InspectData{Commit: "abc123", ReadsPort: true, HealthPath: "/health", Missing: []string{"X"}}, nil
}

func (b *fakeBuilder) Build(_ context.Context, a proto.BuildArgs, secrets map[string][]byte) (proto.BuildData, error) {
	b.got, b.secrets = []string{a.Repo, a.Branch, a.Commit, a.Image, a.Token}, secrets
	if b.err != nil {
		return proto.BuildData{}, b.err
	}
	return proto.BuildData{Commit: "abc123", Subject: b.subject}, nil
}

func cmd(t *testing.T, name string, args any) proto.Command {
	c, err := proto.NewCommand("dep_1:"+name, name, args)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDispatchUnknown(t *testing.T) {
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), proto.Command{Key: "k", Name: "Explode"})
	if res.OK || res.Cause != "unknown command Explode" {
		t.Fatalf("got %+v", res)
	}
}

func TestDispatchStartUsesEnvNetworkAndPort(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdStart, proto.StartArgs{
		Project: "acme", Env: "production", Service: "api", Deploy: "dep_1", Image: "yawl/acme-production-api:dep_1", Kind: "web", Port: 10000,
	}))
	if !res.OK {
		t.Fatalf("got %+v", res)
	}
	var data proto.StartData
	json.Unmarshal(res.Data, &data)
	s := rt.Started[0]
	if s.Network != "yawl-acme-production" || !slices.Contains(s.Env, "PORT=10000") || data.Container == "" {
		t.Fatalf("spec %+v data %+v", s, data)
	}
	if !slices.Contains(rt.Networks, "yawl-acme-production") {
		t.Fatal("network not ensured")
	}
	if s.Cmd != nil {
		t.Fatalf("no command sent, but Cmd = %q: the image's own CMD must run", s.Cmd)
	}
}

func TestDispatchStartPassesCommand(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdStart, proto.StartArgs{
		Project: "acme", Env: "production", Service: "jobs", Deploy: "dep_1", Image: "acme/app:1", Kind: "worker",
		Cmd: []string{"python", "-m", "app.worker"},
	}))
	if !res.OK {
		t.Fatalf("got %+v", res)
	}
	if s := rt.Started[0]; !slices.Equal(s.Cmd, []string{"python", "-m", "app.worker"}) {
		t.Fatalf("Cmd = %q", s.Cmd)
	}
}

func TestDispatchBuild(t *testing.T) {
	b := &fakeBuilder{}
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, b)
	res := d.Handle(context.Background(), cmd(t, proto.CmdBuild, proto.BuildArgs{Repo: "https://x/y", Branch: "main", Image: "yawl/a:1"}))
	var data proto.BuildData
	json.Unmarshal(res.Data, &data)
	if !res.OK || data.Commit != "abc123" || data.Image != "yawl/a:1" {
		t.Fatalf("got %+v %+v", res, data)
	}
}

func TestDispatchSwitchUsesContainerIP(t *testing.T) {
	rt, px := &runtime.Fake{}, &fakeProxy{}
	id, _ := rt.Start(context.Background(), runtime.ContainerSpec{})
	rt.Set(id, runtime.State{Running: true, IP: netip.MustParseAddr("172.18.0.9")})
	d := NewDispatcher(rt, px, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdSwitchRoute, proto.SwitchArgs{Hosts: []string{"api.example"}, Container: id, Port: 10000}))
	if !res.OK || px.routes["api.example"] != "172.18.0.9:10000" {
		t.Fatalf("got %+v %v", res, px.routes)
	}
}

func TestDispatchDrainStopsOld(t *testing.T) {
	rt := &runtime.Fake{}
	id, _ := rt.Start(context.Background(), runtime.ContainerSpec{})
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Clock = fakeClock()
	if res := d.Handle(context.Background(), cmd(t, proto.CmdDrain, proto.DrainArgs{Container: id})); !res.OK {
		t.Fatalf("got %+v", res)
	}
	if !slices.Contains(rt.Stopped, id) {
		t.Fatal("old container not stopped")
	}
	if res := d.Handle(context.Background(), cmd(t, proto.CmdDrain, proto.DrainArgs{})); !res.OK {
		t.Fatal("a first deploy has nothing to drain and must pass")
	}
}

func keyring(t *testing.T) (*secrets.Keyring, func(string) string) {
	kr, err := secrets.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return kr, func(v string) string {
		s, err := seal.Seal(kr.Public, []byte(v))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
}

func TestStartOpensSealedEnv(t *testing.T) {
	rt := &runtime.Fake{}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdStart, proto.StartArgs{Project: "a", Env: "p", Service: "s", Kind: "web", Port: 10000,
		Vars: []proto.SealedVar{{Key: "GREETING", Box: sealTo("hi there")}}}))
	if !res.OK || !slices.Contains(rt.Started[0].Env, "GREETING=hi there") {
		t.Fatalf("%+v %v", res, rt.Started)
	}
}

func TestStartRefusesUnopenable(t *testing.T) {
	rt := &runtime.Fake{}
	kr, _ := keyring(t)
	_, otherSeal := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdStart, proto.StartArgs{Project: "a", Env: "p", Service: "s", Kind: "web",
		Vars: []proto.SealedVar{{Key: "TOKEN", Box: otherSeal("x")}}}))
	if res.OK || len(rt.Started) != 0 || res.Cause != "Cannot open TOKEN: it was sealed for another server key; set it again" {
		t.Fatalf("%+v started=%d", res, len(rt.Started))
	}
}

func TestCrashCauseMasked(t *testing.T) {
	rt := &runtime.Fake{}
	kr, sealTo := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys, d.Clock = kr, fakeClock()
	res := d.Handle(context.Background(), cmd(t, proto.CmdStart, proto.StartArgs{Project: "a", Env: "p", Service: "s", Kind: "web", Port: 10000,
		Vars: []proto.SealedVar{{Key: "TOKEN", Box: sealTo("supersecret1")}}}))
	var data proto.StartData
	json.Unmarshal(res.Data, &data)
	rt.Set(data.Container, runtime.State{Running: false, ExitCode: 1})
	rt.Tails = map[string]string{data.Container: "Error: auth failed for supersecret1\n"}
	res = d.Handle(context.Background(), cmd(t, proto.CmdHealthCheck, proto.HealthArgs{Container: data.Container, Kind: "web", Path: "/", Port: 10000, Timeout: time.Minute}))
	if want := "Error: auth failed for •••• (exit code 1)"; res.Cause != want {
		t.Fatalf("cause %q, want %q", res.Cause, want)
	}
}

func TestRevealReseals(t *testing.T) {
	kr, sealTo := keyring(t)
	pub, priv, _ := seal.GenerateKey()
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdReveal, proto.RevealArgs{Box: sealTo("the value"), To: pub.String()}))
	var data proto.RevealData
	json.Unmarshal(res.Data, &data)
	got, err := seal.Open(pub, priv, data.Box)
	if !res.OK || err != nil || string(got) != "the value" {
		t.Fatalf("%+v %v %q", res, err, got)
	}
}

func TestBuildOpensVars(t *testing.T) {
	b := &fakeBuilder{}
	kr, sealTo := keyring(t)
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, b)
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdBuild, proto.BuildArgs{Repo: "https://x/y", Image: "yawl/a:1", Token: "ghs_t",
		Vars: []proto.SealedVar{{Key: "NPM_TOKEN", Box: sealTo("tok-123456"), Build: true}}}))
	if !res.OK || string(b.secrets["NPM_TOKEN"]) != "tok-123456" || b.got[4] != "ghs_t" {
		t.Fatalf("%+v secrets=%q got=%v", res, b.secrets, b.got)
	}
}

func TestBuildVarWrongKey(t *testing.T) {
	b := &fakeBuilder{}
	kr, _ := keyring(t)
	_, otherSeal := keyring(t)
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, b)
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdBuild, proto.BuildArgs{Repo: "https://x/y", Image: "yawl/a:1",
		Vars: []proto.SealedVar{{Key: "NPM_TOKEN", Box: otherSeal("x"), Build: true}}}))
	if res.OK || b.got != nil || res.Cause != "Cannot open NPM_TOKEN: it was sealed for another server key; set it again" {
		t.Fatalf("%+v builder called=%v", res, b.got != nil)
	}
}

func TestBuildCauseMasked(t *testing.T) {
	b := &fakeBuilder{err: errors.New("build failed: npm ERR! 401 for tok-123456")}
	kr, sealTo := keyring(t)
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, b)
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdBuild, proto.BuildArgs{Repo: "https://x/y", Image: "yawl/a:1",
		Vars: []proto.SealedVar{{Key: "NPM_TOKEN", Box: sealTo("tok-123456"), Build: true}}}))
	if res.Cause != "build failed: npm ERR! 401 for ••••" {
		t.Fatalf("cause %q", res.Cause)
	}
}

func TestBuildSubjectMaskedBeforeCut(t *testing.T) {
	const secret = "tok-1234567890abcdef"
	b := &fakeBuilder{subject: strings.Repeat("é", 195) + secret}
	kr, sealTo := keyring(t)
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, b)
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdBuild, proto.BuildArgs{Repo: "https://x/y", Image: "yawl/a:1",
		Vars: []proto.SealedVar{{Key: "NPM_TOKEN", Box: sealTo(secret), Build: true}}}))
	var data proto.BuildData
	json.Unmarshal(res.Data, &data)
	if data.Subject != strings.Repeat("é", 195)+"••••" {
		t.Fatalf("%+v %q", res, data.Subject)
	}
	d.Keys, b.subject = nil, strings.Repeat("é", 300)
	res = d.Handle(context.Background(), cmd(t, proto.CmdBuild, proto.BuildArgs{Repo: "https://x/y", Image: "yawl/a:1"}))
	json.Unmarshal(res.Data, &data)
	if data.Subject != strings.Repeat("é", 200) {
		t.Fatalf("without keys: %d runes", len([]rune(data.Subject)))
	}
}

func TestDispatchInspect(t *testing.T) {
	b := &fakeBuilder{}
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, b)
	args, _ := json.Marshal(proto.InspectArgs{Repo: "https://x/y", Branch: "main"})
	res := d.Handle(context.Background(), proto.Command{Name: proto.CmdInspect, Args: args})
	var data proto.InspectData
	json.Unmarshal(res.Data, &data)
	if res.Cause != "" || data.Commit != "abc123" || !data.ReadsPort || b.got[0] != "https://x/y" {
		t.Fatalf("%+v %+v", res, data)
	}
	b.err = errors.New("clone https://x/y: not found")
	if res := d.Handle(context.Background(), proto.Command{Name: proto.CmdInspect, Args: args}); res.Cause != "clone https://x/y: not found" {
		t.Fatalf("%+v", res)
	}
	if !proto.Unrecorded(proto.CmdInspect) {
		t.Fatal("Inspect results would fill the idempotency store")
	}
}

func TestDrainMissingContainer(t *testing.T) {
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdDrain, proto.DrainArgs{Container: "yawl-acme-staging-db"}))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
}

func TestStopMissingContainer(t *testing.T) {
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	if res := d.Handle(context.Background(), cmd(t, proto.CmdStop, proto.StopArgs{Container: "yawl-acme-staging-api-dep_1"})); !res.OK {
		t.Fatalf("%+v", res)
	}
}

func TestStartMountsVolumes(t *testing.T) {
	rt := &runtime.Fake{}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	c, _ := proto.NewCommand("s", proto.CmdStart, proto.StartArgs{Project: "acme", Env: "production", Service: "api", Deploy: "d1", Image: "i", Kind: "web", Port: 10000,
		Volumes: []proto.Volume{{Name: "uploads", Path: "/app/uploads"}}})
	if res := d.Handle(context.Background(), c); !res.OK {
		t.Fatalf("%+v", res)
	}
	m := rt.Started[0].Mounts
	if len(m) != 1 || m[0] != (runtime.Mount{Source: "yawl_acme_production_api_uploads", Target: "/app/uploads", Volume: true}) {
		t.Fatalf("%+v", m)
	}
}

func TestCleanupCommands(t *testing.T) {
	rt := &runtime.Fake{Unused: []string{"yawl-old-data"}}
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	res := d.Handle(context.Background(), cmd(t, proto.CmdCleanup, proto.CleanupArgs{AllCache: true}))
	var data proto.CleanupData
	json.Unmarshal(res.Data, &data)
	if !res.OK || data.Containers != 2 || data.Freed != 3<<20 || len(rt.Cleanups) != 1 || !rt.Cleanups[0].AllCache ||
		rt.Cleanups[0].StoppedFor != time.Hour || rt.Cleanups[0].CacheFor != 7*24*time.Hour {
		t.Fatalf("%+v %+v %+v", res, data, rt.Cleanups)
	}
	res = d.Handle(context.Background(), cmd(t, proto.CmdUnusedVolumes, struct{}{}))
	var v proto.UnusedVolumesData
	json.Unmarshal(res.Data, &v)
	if !res.OK || len(v.Names) != 1 || v.Names[0] != "yawl-old-data" {
		t.Fatalf("%+v", res)
	}
}
