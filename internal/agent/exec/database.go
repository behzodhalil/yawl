package exec

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

const walDir = "/var/lib/postgresql/data/yawl_wal"

var dbReady = 60 * time.Second

// dbStopGrace lets a database flush before Docker kills it: 10 s could cut
// mysqld mid flush and leave it crash recovery at its next start (Plan 28 minor).
const dbStopGrace = time.Minute

const notReady = " did not become ready in "

type engine struct {
	image     func(version int) string
	dataDir   string
	env       func(pw string) []string
	cmd       func(pw string, memory int64) []string
	clientEnv func(host, pw string) []string
	ready     func(host, pw string) []string
	url       func(host, pw string) string
	dump      func(host string) []string
	load      func(host string) []string
	within    time.Duration
	files     func(pw string) []runtime.File
	init      func(host string) []string
	primary   func(host string) []string
}

func mongoKey(pw string) []byte {
	sum := sha256.Sum256([]byte("yawl-mongo-key:" + pw))
	return []byte(hex.EncodeToString(sum[:]))
}

func mongoShell(script string) []string {
	return []string{"mongosh", "--nodb", "--quiet", "--eval", "db = connect(process.env.MONGO_URL);\n" + script}
}

// mongoTool runs mongodump or mongorestore with $MONGO_URL in a --config file
// that printf (a shell builtin, so no argv of its own) writes (Plan 29 minors).
func mongoTool(args string) []string {
	return []string{"sh", "-c", `umask 077 && printf "uri: '%s'\n" "$MONGO_URL" >/tmp/yawl-mongo.yaml && exec ` + args + ` --config /tmp/yawl-mongo.yaml`}
}

var engines = map[string]engine{
	"mongodb": { // 8 is 8.0; a replica set of one, rs0, for transactions and Prisma (record 29, B6)
		image:   func(int) string { return "mongo:8.0" },
		dataDir: "/data/db",
		env: func(pw string) []string {
			return []string{"MONGO_INITDB_ROOT_USERNAME=app", "MONGO_INITDB_ROOT_PASSWORD=" + pw}
		},
		cmd: func(_ string, memory int64) []string {
			c := []string{"mongod", "--replSet", "rs0", "--bind_ip_all", "--keyFile", "/etc/yawl-mongo/key"}
			if memory > 0 { // half of what mongod's own 256MB leaves, at least WiredTiger's 0.25GB (Plan 29 review I2)
				gb := max(0.25, float64(memory-256<<20)/2/(1<<30))
				c = append(c, "--wiredTigerCacheSizeGB", strconv.FormatFloat(gb, 'f', 2, 64))
			}
			return c
		},
		files: func(pw string) []runtime.File {
			return []runtime.File{{Path: "/etc/yawl-mongo/key", Mode: 0o400, UID: 999, Data: mongoKey(pw)}}
		},
		clientEnv: func(h, pw string) []string {
			return []string{"MONGO_URL=mongodb://app:" + pw + "@" + h + ":27017/?authSource=admin&directConnection=true"}
		},
		ready: func(string, string) []string { return mongoShell("db.runCommand({ping: 1}).ok") },
		init: func(h string) []string {
			return mongoShell(`try { rs.status().ok } catch (e) { rs.initiate({_id: "rs0", members: [{_id: 0, host: "` + h + `:27017"}]}).ok }`)
		},
		primary: func(string) []string { return mongoShell("db.hello().isWritablePrimary") },
		url: func(h, pw string) string {
			return "mongodb://app:" + pw + "@" + h + ":27017/app?authSource=admin&replicaSet=rs0"
		},
		// every database the app made, whatever it named it (review I1); not --oplog: mongorestore refuses
		// --oplogReplay with the excludes that keep the restored copy's own users and password
		dump: func(string) []string { return mongoTool("mongodump --archive") },
		load: func(string) []string {
			return mongoTool("mongorestore --archive --drop --nsExclude 'admin.*' --nsExclude 'config.*'")
		},
		within: 3 * time.Minute,
	},
	"mysql": {
		image:   func(int) string { return "mysql:8.4" },
		dataDir: "/var/lib/mysql",
		env: func(pw string) []string {
			return []string{"MYSQL_DATABASE=app", "MYSQL_USER=app", "MYSQL_PASSWORD=" + pw, "MYSQL_ROOT_PASSWORD=" + pw, "MYSQL_ROOT_HOST=localhost"}
		},
		clientEnv: func(_, pw string) []string { return []string{"MYSQL_PWD=" + pw} },
		ready:     func(h, _ string) []string { return []string{"mysql", "-h", h, "-u", "app", "-e", "SELECT 1", "app"} }, // a login, not a ping: an init cut short leaves no app user
		url:       func(h, pw string) string { return "mysql://app:" + pw + "@" + h + ":3306/app" },
		dump: func(h string) []string {
			return []string{"mysqldump", "--single-transaction", "--no-tablespaces", "--routines", "--triggers", "--events", "-h", h, "-u", "app", "app"}
		},
		load:   func(h string) []string { return []string{"mysql", "-h", h, "-u", "app", "app"} },
		within: 3 * time.Minute, // the first start initialises the data directory with networking off
	},
	"mariadb": {
		image:   func(int) string { return "mariadb:11.4" },
		dataDir: "/var/lib/mysql",
		env: func(pw string) []string {
			return []string{"MARIADB_DATABASE=app", "MARIADB_USER=app", "MARIADB_PASSWORD=" + pw, "MARIADB_ROOT_PASSWORD=" + pw, "MARIADB_ROOT_HOST=localhost"}
		},
		clientEnv: func(_, pw string) []string { return []string{"MYSQL_PWD=" + pw} }, // the mariadb client reads it too
		ready:     func(h, _ string) []string { return []string{"mariadb", "-h", h, "-u", "app", "-e", "SELECT 1", "app"} },
		url:       func(h, pw string) string { return "mysql://app:" + pw + "@" + h + ":3306/app" },
		dump: func(h string) []string {
			return []string{"mariadb-dump", "--single-transaction", "--routines", "--triggers", "--events", "-h", h, "-u", "app", "app"}
		},
		load:   func(h string) []string { return []string{"mariadb", "-h", h, "-u", "app", "app"} },
		within: 3 * time.Minute,
	},
	"clickhouse": {
		image:   func(int) string { return "clickhouse/clickhouse-server:25.8" },
		dataDir: "/var/lib/clickhouse",
		env: func(pw string) []string {
			return []string{"CLICKHOUSE_DB=app", "CLICKHOUSE_USER=app", "CLICKHOUSE_PASSWORD=" + pw, "CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1"}
		},
		clientEnv: func(_, pw string) []string { return []string{"CLICKHOUSE_PASSWORD=" + pw} },
		ready: func(h, _ string) []string {
			return []string{"clickhouse-client", "--host", h, "--user", "app", "--query", "SELECT 1"}
		},
		url:    func(h, pw string) string { return "http://app:" + pw + "@" + h + ":8123/app" },
		dump:   func(h string) []string { return []string{"sh", "-c", chDump, "sh", h} },
		load:   func(h string) []string { return []string{"sh", "-c", chLoad, "sh", h} },
		within: 3 * time.Minute,
	},
	"valkey": {
		image:   func(v int) string { return "valkey/valkey:" + strconv.Itoa(v) },
		dataDir: "/data",
		cmd: func(pw string, memory int64) []string {
			c := []string{"valkey-server", "--requirepass", pw, "--appendonly", "yes", "--appendfsync", "everysec", "--maxmemory-policy", "noeviction"}
			if memory > 0 { // noeviction refuses writes only below maxmemory; a quarter stays free for rewrites and clients (review C1)
				c = append(c, "--maxmemory", strconv.FormatInt(memory*3/4, 10))
			}
			return c
		},
		ready: func(h, pw string) []string {
			return []string{"valkey-cli", "-h", h, "-a", pw, "--no-auth-warning", "ping"}
		},
		url: func(h, pw string) string { return "redis://default:" + pw + "@" + h + ":6379" },
	},
}

const chObjects = `SELECT engine IN ('View', 'MaterializedView', 'LiveView', 'WindowView', 'Dictionary') AS v, ` +
	`engine LIKE '%MergeTree' OR engine IN ('Log', 'TinyLog', 'StripeLog', 'Memory', 'EmbeddedRocksDB'), name ` +
	`FROM system.tables WHERE database = 'app' AND NOT startsWith(name, '.inner') ORDER BY v, name FORMAT TSVRaw`

// chShell is what both scripts share: a client that never reads the
// script's stdin, its password from CLICKHOUSE_PASSWORD (clientEnv), and q,
// which escapes a name for inside '...' or "..." in a query.
const chShell = `set -eu
h=$1
c() { clickhouse-client --host "$h" --user app "$@" </dev/null; }
q() { printf '%s' "$1" | sed "s/[\\\"']/\\\\&/g"; }
`

// chDump writes each object's CREATE statement and, for a table that keeps
// rows, its rows in Native, zstd'd, into a folder, then packs it as one tar
// on stdout: a tar must know each file's size first, so the folder takes
// disk about the size of the compressed data (record 65, C1 and C6).
const chDump = chShell + `l=$(mktemp) d=$(mktemp -d)
c --query "` + chObjects + `" >"$l"
cd "$d"
i=0
while IFS=$(printf '\t') read -r v r n <&3; do
	i=$((i+1)) p=t
	[ "$v" = 0 ] || p=v
	p=$p$(printf '%05d' "$i")
	printf '%s' "$n" >"$p.name"
	c --query "SELECT create_table_query FROM system.tables WHERE database = 'app' AND name = '$(q "$n")' FORMAT TSVRaw" >"$p.sql"
	[ "$r" = 0 ] || c --query "SELECT * FROM app.\"$(q "$n")\" INTO OUTFILE '$p.native.zst' COMPRESSION 'zstd' FORMAT Native"
done 3<"$l"
tar -cf - .
`

// chLoad reads chDump's tar on stdin into an emptied app: tables, their
// rows, then views. Rows go in before the views so a materialized view does
// not count them a second time into its TO table (record 65, C2). Each group
// is made in rounds while one more goes in, for a view that reads a view.
const chLoad = chShell + `d=$(mktemp -d)
cd "$d"
tar -xf -
create() {
	[ -e "$1" ] || return 0
	while [ $# -gt 0 ]; do
		n=$# left=
		for f; do c --query "$(cat "$f")" 2>"$f.err" || left="$left $f"; done
		set -- $left
		[ $# -lt $n ] || { cat "$1.err" >&2; exit 1; }
	done
}
c --query "DROP DATABASE IF EXISTS app SYNC"
c --query "CREATE DATABASE app"
create t*.sql
for f in t*.native.zst; do
	[ -e "$f" ] || continue
	c --query "INSERT INTO app.\"$(q "$(cat "${f%.native.zst}.name")")\" FROM INFILE '$f' COMPRESSION 'zstd' FORMAT Native"
done
create v*.sql
`

func isPostgres(kind string) bool { return kind == "" || kind == "postgres" }

func dbNames(kind, project, env, name string, version int) (container, volume, network, image string) {
	container = fmt.Sprintf("yawl-%s-%s-%s", project, env, name)
	// Postgres with pgvector to CREATE EXTENSION (design record 62), on the Debian of postgres:N
	// today: another glibc could sort text differently and spoil its indexes (Plan 63 review I8)
	image = "pgvector/pgvector:pg" + strconv.Itoa(version) + "-trixie"
	if e, ok := engines[kind]; ok {
		image = e.image(version)
	}
	return container, container + "-data", "yawl-" + project + "-" + env, image
}

func dbURL(kind, name, password string) string {
	if e, ok := engines[kind]; ok {
		return e.url(name, password)
	}
	return "postgres://app:" + password + "@" + name + ":5432/app?sslmode=disable"
}

func (d *Dispatcher) ensureDatabase(ctx context.Context, a proto.EnsureDatabaseArgs) proto.Result {
	if _, ok := engines[a.Kind]; !ok && !isPostgres(a.Kind) {
		return proto.Result{Cause: "this agent does not run " + a.Kind + "; update it"}
	}
	container, volume, network, image := dbNames(a.Kind, a.Project, a.Env, a.Name, a.Version)
	if err := d.rt.EnsureNetwork(ctx, network); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	var data proto.EnsureDatabaseData
	var password string
	if a.Password != "" {
		pw, cause := d.open(proto.SealedVar{Key: "PASSWORD", Box: a.Password})
		if cause != "" {
			return proto.Result{Cause: cause}
		}
		password = string(pw)
	} else if kept, ok := d.keptBoxes(volume); ok {
		pw, cause := d.open(proto.SealedVar{Key: "PASSWORD", Box: kept.PasswordBox})
		if cause != "" {
			return proto.Result{Cause: cause}
		}
		password, data = string(pw), kept
	} else {
		if d.Keys == nil {
			return proto.Result{Cause: "this agent has no secrets key; re-join it"}
		}
		if exists, err := d.rt.VolumeExists(ctx, volume); err != nil {
			return proto.Result{Cause: err.Error()}
		} else if exists {
			return proto.Result{Cause: a.Name + " has data but Yawl has no password for it; it was created outside this server"}
		}
		rec, err := seal.ParsePublicKey(a.RecoveryPubkey)
		if err != nil {
			return proto.Result{Cause: "bad recovery key: " + err.Error()}
		}
		b := make([]byte, 32)
		rand.Read(b)
		password = base64.RawURLEncoding.EncodeToString(b)
		d.Keys.Masker().Add(password)
		url := dbURL(a.Kind, a.Name, password)
		for _, s := range []struct {
			to  seal.PublicKey
			val string
			out *string
		}{{d.Keys.Public, password, &data.PasswordBox}, {rec, password, &data.PasswordRecoveryBox},
			{d.Keys.Public, url, &data.URLBox}, {rec, url, &data.URLRecoveryBox}} {
			if *s.out, err = seal.Seal(s.to, []byte(s.val)); err != nil {
				return proto.Result{Cause: err.Error()}
			}
		}
		if err := d.keepBoxes(volume, data); err != nil {
			return proto.Result{Cause: "cannot keep the database's sealed password: " + err.Error()}
		}
	}
	spec, cause := runtime.ContainerSpec{}, ""
	if e, ok := engines[a.Kind]; ok {
		spec, cause = engineSpec(e, a, container, volume, network, image, password)
	} else {
		spec, cause = d.dbSpec(a, container, volume, network, image, password)
	}
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	st, err := d.rt.Inspect(ctx, container)
	stale := err == nil && (st.Labels["yawl.pgconf"] != spec.Labels["yawl.pgconf"] || st.Labels["yawl.limits"] != spec.Labels["yawl.limits"] ||
		st.Labels["yawl.cmd"] != "" && st.Labels["yawl.cmd"] != spec.Labels["yawl.cmd"])
	if err == nil && (!st.Running || stale) {
		if perr := d.rt.Prepare(ctx, spec.Image); perr != nil {
			return proto.Result{Cause: fmt.Sprintf("Cannot get %s: %v. %s keeps running as it was.", spec.Image, perr, a.Name)}
		}
		if st.Running {
			d.rt.Stop(ctx, container, dbStopGrace)
		}
		d.rt.Remove(ctx, container)
		if st.Labels["yawl.pitr"] == "1" && !a.PITR {
			d.rt.RunOnce(ctx, walHelper(volume, false, "rm", "-rf", walIn), nil, nil)
		}
	}
	if err != nil || !st.Running || stale {
		if _, err := d.rt.Start(ctx, spec); err != nil {
			if len(spec.Ports) == 0 {
				return proto.Result{Cause: err.Error()}
			}
			d.rt.Remove(ctx, container)
			spec.Ports = nil
			spec.Labels["yawl.pgconf"] = "unpublished"
			if _, err2 := d.rt.Start(ctx, spec); err2 != nil {
				return proto.Result{Cause: err2.Error()}
			}
			d.waitReady(ctx, a.Kind, image, network, a.Name, password)
			return proto.Result{Cause: fmt.Sprintf("Cannot publish port %d: %v. %s runs without outside access.", a.OutsidePort, err, a.Name)}
		}
	}
	if a.OutsidePort != 0 {
		rec, err := seal.ParsePublicKey(a.RecoveryPubkey)
		if err != nil {
			return proto.Result{Cause: "bad recovery key: " + err.Error()}
		}
		url := fmt.Sprintf("postgres://app:%s@%s/app?sslmode=require", password, net.JoinHostPort(a.PublicIP, strconv.Itoa(a.OutsidePort)))
		if data.OutsideURLBox, err = seal.Seal(d.Keys.Public, []byte(url)); err != nil {
			return proto.Result{Cause: err.Error()}
		}
		if data.OutsideURLRecoveryBox, err = seal.Seal(rec, []byte(url)); err != nil {
			return proto.Result{Cause: err.Error()}
		}
	}
	if len(a.ShareWith) > 0 && a.PrivatePort != 0 {
		url := fmt.Sprintf("postgres://app:%s@%s/app?sslmode=require", password, net.JoinHostPort(a.PrivateIP, strconv.Itoa(a.PrivatePort)))
		data.Shares = map[string]string{}
		for server, key := range a.ShareWith {
			pk, err := seal.ParsePublicKey(key)
			if err != nil {
				return proto.Result{Cause: fmt.Sprintf("the agent key of %s: %v", server, err)}
			}
			if data.Shares[server], err = seal.Seal(pk, []byte(url)); err != nil {
				return proto.Result{Cause: err.Error()}
			}
		}
	}
	if a.ShareHere {
		url := "postgres://app:" + password + "@" + container + ":5432/app?sslmode=disable"
		var err error
		if data.Here, err = seal.Seal(d.Keys.Public, []byte(url)); err != nil {
			return proto.Result{Cause: err.Error()}
		}
	}
	if cause := d.waitReady(ctx, a.Kind, image, network, a.Name, password); cause != "" {
		return proto.Result{Cause: cause}
	}
	if e, found := engines[a.Kind]; found && e.init != nil {
		if cause := d.initEngine(ctx, e, image, network, a.Name, password); cause != "" {
			return proto.Result{Cause: cause}
		}
	}
	return ok(data)
}

func (d *Dispatcher) dbSpec(a proto.EnsureDatabaseArgs, container, volume, network, image, password string) (runtime.ContainerSpec, string) {
	allow, err := parseAllow(a.Allow)
	if err != nil {
		return runtime.ContainerSpec{}, err.Error()
	}
	if a.PrivatePort != 0 {
		allow = append(allow, meshPrefix)
	}
	subnet, gw, err := d.rt.NetworkInfo(context.Background(), network)
	if err != nil {
		return runtime.ContainerSpec{}, err.Error()
	}
	ip, _ := netip.ParseAddr(a.PublicIP)
	dir := ""
	if d.DBDir != "" {
		dir = filepath.Join(d.DBDir, container+"-conf")
	}
	cert, key, err := tlsPair(dir, ip)
	if err != nil {
		return runtime.ContainerSpec{}, "cannot make the database's certificate: " + err.Error()
	}
	if cause := checkCPU(a.Name, a.CPU, goruntime.NumCPU()); cause != "" {
		return runtime.ContainerSpec{}, cause
	}
	s := runtime.ContainerSpec{
		Name: container, Image: image, Network: network, Aliases: []string{a.Name}, CPU: a.CPU, Memory: a.Memory,
		// PGDATA as 16 and 17 have it: 18 moved it and refuses a volume at the old path
		Env:    []string{"POSTGRES_USER=app", "POSTGRES_DB=app", "POSTGRES_PASSWORD=" + password, "PGDATA=/var/lib/postgresql/data"},
		Mounts: []runtime.Mount{{Source: volume, Target: "/var/lib/postgresql/data", Volume: true}},
		Labels: map[string]string{"yawl.project": a.Project, "yawl.env": a.Env, "yawl.service": a.Name, "yawl.pgconf": pgconfHash(a.OutsidePort, a.Allow, a.PrivateIP, a.PrivatePort, a.PITR)},
		Cmd: []string{"postgres", "-c", "ssl=on", "-c", "ssl_cert_file=" + pgConfDir + "/server.crt",
			"-c", "ssl_key_file=" + pgConfDir + "/server.key", "-c", "hba_file=" + pgConfDir + "/pg_hba.conf"},
		Files: []runtime.File{
			{Path: pgConfDir + "/pg_hba.conf", Mode: 0o600, UID: pgUID, Data: []byte(HBA(allow, gw, subnet, a.PITR))},
			{Path: pgConfDir + "/server.crt", Mode: 0o600, UID: pgUID, Data: cert},
			{Path: pgConfDir + "/server.key", Mode: 0o600, UID: pgUID, Data: key},
		},
	}
	if a.PITR {
		s.Labels["yawl.pitr"] = "1"
		s.Cmd = append(s.Cmd, "-c", "archive_mode=on", "-c", "archive_timeout=60",
			"-c", "archive_command=mkdir -p "+walDir+" && test ! -f "+walDir+"/%f && cp %p "+walDir+"/%f.tmp && mv "+walDir+"/%f.tmp "+walDir+"/%f") // a cut copy never has the final name
	}
	if a.CPU != 0 || a.Memory != 0 {
		s.Labels["yawl.limits"] = fmt.Sprintf("%g/%d", a.CPU, a.Memory)
	}
	if a.OutsidePort != 0 {
		s.Ports = map[int]int{a.OutsidePort: 5432}
	}
	if a.PrivatePort != 0 {
		if _, err := netip.ParseAddr(a.PrivateIP); err != nil || !meshPrefix.Contains(netip.MustParseAddr(a.PrivateIP)) {
			return runtime.ContainerSpec{}, fmt.Sprintf("private address %q is not on the private network", a.PrivateIP)
		}
		if s.Ports == nil {
			s.Ports = map[int]int{}
		}
		s.Ports[a.PrivatePort] = 5432
		s.PortIP = map[int]string{a.PrivatePort: a.PrivateIP}
	}
	return s, ""
}

func engineSpec(e engine, a proto.EnsureDatabaseArgs, container, volume, network, image, password string) (runtime.ContainerSpec, string) {
	if cause := checkCPU(a.Name, a.CPU, goruntime.NumCPU()); cause != "" {
		return runtime.ContainerSpec{}, cause
	}
	s := runtime.ContainerSpec{
		Name: container, Image: image, Network: network, Aliases: []string{a.Name}, CPU: a.CPU, Memory: a.Memory,
		Mounts: []runtime.Mount{{Source: volume, Target: e.dataDir, Volume: true}},
		Labels: map[string]string{"yawl.project": a.Project, "yawl.env": a.Env, "yawl.service": a.Name},
	}
	if e.cmd != nil {
		s.Cmd = e.cmd(password, a.Memory)
		sum := sha256.Sum256([]byte(strings.Join(s.Cmd, "\x00")))
		s.Labels["yawl.cmd"] = hex.EncodeToString(sum[:8])
	}
	if e.env != nil {
		s.Env = e.env(password)
	}
	if e.files != nil {
		s.Files = e.files(password)
	}
	if a.CPU != 0 || a.Memory != 0 {
		s.Labels["yawl.limits"] = fmt.Sprintf("%g/%d", a.CPU, a.Memory)
	}
	return s, ""
}

func (d *Dispatcher) waitReady(ctx context.Context, kind, image, network, host, password string) string {
	wait := dbReady
	if e, ok := engines[kind]; ok && e.within > wait {
		wait = e.within
	}
	deadline := time.Now().Add(wait)
	last := ""
	probe := runtime.ContainerSpec{Image: image, Network: network, Cmd: []string{"pg_isready", "-h", host, "-U", "app"}}
	if e, ok := engines[kind]; ok {
		probe.Cmd = e.ready(host, password)
		if e.clientEnv != nil {
			probe.Env = e.clientEnv(host, password)
		}
	}
	for {
		exit, stderr, err := d.rt.RunOnce(ctx, probe, nil, nil)
		if err == nil && exit == 0 {
			return ""
		}
		if last = stderr; err != nil {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return fmt.Sprintf("%s"+notReady+"%d seconds: %s", host, int(wait.Seconds()), last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err().Error()
		case <-time.After(time.Second):
		}
	}
}

func (d *Dispatcher) keepBoxes(volume string, data proto.EnsureDatabaseData) error {
	if d.DBDir == "" {
		return nil
	}
	if err := os.MkdirAll(d.DBDir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(data)
	return os.WriteFile(filepath.Join(d.DBDir, volume+".json"), b, 0o600)
}

func (d *Dispatcher) keptBoxes(volume string) (proto.EnsureDatabaseData, bool) {
	var data proto.EnsureDatabaseData
	if d.DBDir == "" {
		return data, false
	}
	b, err := os.ReadFile(filepath.Join(d.DBDir, volume+".json"))
	if err != nil || json.Unmarshal(b, &data) != nil || data.PasswordBox == "" {
		return data, false
	}
	return data, true
}

func (d *Dispatcher) initEngine(ctx context.Context, e engine, image, network, host, pw string) string {
	exit, stderr, err := d.rt.RunOnce(ctx, clientSpec(e, image, network, host, pw, e.init(host)), nil, nil)
	if err != nil {
		return host + ": " + err.Error()
	}
	if exit != 0 {
		return fmt.Sprintf("%s: setting it up exited %d: %s", host, exit, stderr)
	}
	deadline := time.Now().Add(max(e.within, dbReady))
	for {
		var out bytes.Buffer
		if exit, _, err := d.rt.RunOnce(ctx, clientSpec(e, image, network, host, pw, e.primary(host)), nil, &out); err == nil && exit == 0 && strings.Contains(out.String(), "true") {
			return ""
		}
		if time.Now().After(deadline) {
			return host + " did not become writable"
		}
		select {
		case <-ctx.Done():
			return ctx.Err().Error()
		case <-time.After(time.Second):
		}
	}
}
