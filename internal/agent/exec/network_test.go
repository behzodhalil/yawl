package exec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

func privateEnsure(t *testing.T) (*runtime.Fake, proto.Result, seal.PublicKey, seal.PrivateKey) {
	rt := &runtime.Fake{}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	other, otherPriv, _ := seal.GenerateKey()
	args, _, _ := ensureArgs(t, "")
	args.PrivateIP, args.PrivatePort, args.ShareWith = "10.73.0.1", 15432, map[string]string{"srv_2": other.String()}
	return rt, d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, args)), other, otherPriv
}

func TestPrivatePortBindsPrivateIP(t *testing.T) {
	rt, res, _, _ := privateEnsure(t)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	s := rt.Started[0]
	if s.Ports[15432] != 5432 || s.PortIP[15432] != "10.73.0.1" || len(s.Ports) != 1 {
		t.Fatalf("ports %v on %v", s.Ports, s.PortIP)
	}
}

func TestHBAAllowsMeshOverTLS(t *testing.T) {
	rt, _, _, _ := privateEnsure(t)
	hba := ""
	for _, f := range rt.Started[0].Files {
		if strings.HasSuffix(f.Path, "pg_hba.conf") {
			hba = string(f.Data)
		}
	}
	mesh, reject := strings.Index(hba, "hostssl app app 10.73.0.0/16 scram-sha-256\n"), strings.Index(hba, "reject")
	if mesh < 0 || mesh > reject {
		t.Fatalf("%s", hba)
	}
}

func TestShareSealedToReferencingServer(t *testing.T) {
	_, res, other, otherPriv := privateEnsure(t)
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	url, err := seal.Open(other, otherPriv, data.Shares["srv_2"])
	if err != nil || !strings.HasPrefix(string(url), "postgres://app:") || !strings.HasSuffix(string(url), "@10.73.0.1:15432/app?sslmode=require") {
		t.Fatalf("%q %v", url, err)
	}
}

func TestShareHereUsesContainerName(t *testing.T) {
	rt := &runtime.Fake{}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	args, _, _ := ensureArgs(t, "")
	args.ShareHere = true
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, args))
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	url, err := kr.Open(data.Here)
	if err != nil || !strings.HasSuffix(string(url), "@yawl-acme-production-acme-db:5432/app?sslmode=disable") || len(rt.Started[0].Ports) != 0 {
		t.Fatalf("%q %v %v", url, err, rt.Started[0].Ports)
	}
}
