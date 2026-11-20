package fw

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/behzodhalil/yawl/internal/proto"
)

func TestListening(t *testing.T) {
	proc := t.TempDir()
	os.CopyFS(proc, os.DirFS("testdata/proc"))
	os.MkdirAll(filepath.Join(proc, "123", "fd"), 0o755)
	os.WriteFile(filepath.Join(proc, "123", "comm"), []byte("sshd\n"), 0o644)
	os.Symlink("socket:[1111]", filepath.Join(proc, "123", "fd", "3"))
	got, err := Listening(proc)
	if err != nil {
		t.Fatal(err)
	}
	want := []proto.FwPort{
		{Proto: "tcp", Port: 22, Address: "0.0.0.0", Process: "sshd"},
		{Proto: "tcp", Port: 5432, Address: "::"},
		{Proto: "tcp", Port: 6379, Address: "0.0.0.0"},
		{Proto: "tcp", Port: 7000, Address: "127.0.0.1"},
		{Proto: "udp", Port: 51820, Address: "0.0.0.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestClassify(t *testing.T) {
	ports := []proto.FwPort{
		{Proto: "tcp", Port: 8080, Address: "0.0.0.0"}, {Proto: "tcp", Port: 443, Address: "::"},
		{Proto: "tcp", Port: 6379, Address: "0.0.0.0"}, {Proto: "tcp", Port: 7000, Address: "127.0.0.1"},
		{Proto: "tcp", Port: 22, Address: "0.0.0.0"}, {Proto: "tcp", Port: 2222, Address: "0.0.0.0"},
	}
	a := proto.FirewallArgs{On: true, Allow: []proto.FwRule{{Proto: "tcp", Port: 8080, From: []string{"203.0.113.0/24"}}}, SSHFrom: []string{"192.0.2.10"}}
	got := map[int]string{}
	for _, p := range Classify(ports, a, []int{22, 2222}) {
		got[p.Port] = p.Exposure
	}
	if !reflect.DeepEqual(got, map[int]string{8080: "some", 443: "everyone", 6379: "closed", 22: "some", 2222: "some"}) {
		t.Fatalf("on: %v", got)
	}
	off := Classify(ports, proto.FirewallArgs{}, []int{22})
	if len(off) != 5 || off[0].Exposure != "everyone" {
		t.Fatalf("off: %+v", off)
	}
}
