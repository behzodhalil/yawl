package exec

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func mongoArgs(t *testing.T) proto.EnsureDatabaseArgs {
	a, _, _ := ensureArgs(t, "")
	a.Kind, a.Name, a.Version = "mongodb", "docs", 8
	return a
}

func primaryAnswers(s runtime.ContainerSpec, _ []byte) (string, int, string, error) {
	if strings.Contains(strings.Join(s.Cmd, " "), "isWritablePrimary") {
		return "true\n", 0, "", nil
	}
	return "", 0, "", nil
}

func TestEnsureMongoFake(t *testing.T) {
	rt := &runtime.Fake{OnRun: primaryAnswers}
	kr, _ := keyring(t)
	d := NewDispatcher(rt, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdEnsureDatabase, mongoArgs(t)))
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	var data proto.EnsureDatabaseData
	json.Unmarshal(res.Data, &data)
	pw, _ := kr.Open(data.PasswordBox)
	url, _ := kr.Open(data.URLBox)
	if string(url) != "mongodb://app:"+string(pw)+"@docs:27017/app?authSource=admin&replicaSet=rs0" {
		t.Fatalf("url %q", url)
	}
	s := rt.Started[0]
	if s.Image != "mongo:8.0" || s.Mounts[0].Target != "/data/db" ||
		strings.Join(s.Cmd, " ") != "mongod --replSet rs0 --bind_ip_all --keyFile /etc/yawl-mongo/key" ||
		!slices.Contains(s.Env, "MONGO_INITDB_ROOT_USERNAME=app") || !slices.Contains(s.Env, "MONGO_INITDB_ROOT_PASSWORD="+string(pw)) {
		t.Fatalf("spec %+v", s)
	}
	if len(s.Files) != 1 || s.Files[0].Path != "/etc/yawl-mongo/key" || s.Files[0].Mode != 0o400 || s.Files[0].UID != 999 ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).Match(s.Files[0].Data) {
		t.Fatalf("key file %+v", s.Files)
	}
	if again := engines["mongodb"].files(string(pw)); string(again[0].Data) != string(s.Files[0].Data) {
		t.Fatal("the key file changes for the same password")
	}
	var initiate, primary int
	for _, r := range rt.Ran {
		c := strings.Join(r.Spec.Cmd, " ")
		if strings.Contains(c, string(pw)) {
			t.Fatalf("a password in argv: %s", c)
		}
		if strings.Contains(c, "rs.initiate") {
			initiate++
			if !strings.Contains(c, "rs.status().ok") || !strings.Contains(c, `host: "docs:27017"`) || !slices.Contains(r.Spec.Env, "MONGO_URL=mongodb://app:"+string(pw)+"@docs:27017/?authSource=admin&directConnection=true") {
				t.Fatalf("initiate %+v", r.Spec)
			}
		}
		if strings.Contains(c, "isWritablePrimary") {
			primary++
		}
	}
	if initiate != 1 || primary < 1 {
		t.Fatalf("initiate %d, primary polls %d", initiate, primary)
	}
}

func TestMongoDumpAndLoad(t *testing.T) {
	e := engines["mongodb"]
	d := clientSpec(e, "mongo:8.0", "n", "docs", "pw", e.dump("docs"))
	l := clientSpec(e, "mongo:8.0", "n", "docs2", "pw2", e.load("docs2"))
	if !strings.Contains(d.Cmd[2], "exec mongodump --archive --config /tmp/yawl-mongo.yaml") || !slices.Contains(d.Env, "MONGO_URL=mongodb://app:pw@docs:27017/?authSource=admin&directConnection=true") {
		t.Fatalf("dump %+v", d)
	}
	if !strings.Contains(l.Cmd[2], "exec mongorestore --archive --drop --nsExclude 'admin.*' --nsExclude 'config.*' --config /tmp/yawl-mongo.yaml") || !slices.Contains(l.Env, "MONGO_URL=mongodb://app:pw2@docs2:27017/?authSource=admin&directConnection=true") {
		t.Fatalf("load %+v", l)
	}
}

func TestMongoURLNeverInArgv(t *testing.T) {
	e := engines["mongodb"]
	for _, c := range [][]string{e.ready("docs", "pw"), e.init("docs"), e.primary("docs"), e.dump("docs"), e.load("docs")} {
		j := strings.Join(c, " ")
		if strings.Contains(j, "--uri") || strings.Contains(j, `mongosh "$MONGO_URL"`) {
			t.Fatalf("the URL reaches a tool's argv: %s", j)
		}
		if c[0] == "sh" && !strings.Contains(j, `printf "uri: '%s'\n" "$MONGO_URL" >/tmp/yawl-mongo.yaml`) {
			t.Fatalf("not through the config file: %s", j)
		}
	}
	script := "print(`$HOME`)"
	if c := mongoShell(script); c[0] != "mongosh" || c[len(c)-1] != "db = connect(process.env.MONGO_URL);\n"+script {
		t.Fatalf("the script is not mongosh's as written: %q", c)
	}
}

func TestMongoCacheFollowsLimit(t *testing.T) {
	e := engines["mongodb"]
	if c := strings.Join(e.cmd("pw", 2<<30), " "); !strings.Contains(c, "--wiredTigerCacheSizeGB 0.88") {
		t.Fatalf("2GB: %s", c)
	}
	if c := strings.Join(e.cmd("pw", 512<<20), " "); !strings.Contains(c, "--wiredTigerCacheSizeGB 0.25") {
		t.Fatalf("512MB: %s", c)
	}
	if c := strings.Join(e.cmd("pw", 0), " "); strings.Contains(c, "wiredTiger") {
		t.Fatalf("no limit: %s", c)
	}
}
