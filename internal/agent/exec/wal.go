package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/backup"
	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/seal"
)

var walName = regexp.MustCompile(`^([0-9A-F]{24}(\.[0-9A-F]{8}\.backup)?|[0-9A-F]{8}\.history)$`)

// walQuiet is how old a file must be to ship: cp writes the final name, so a
// younger one may still be growing.
const walQuiet = 10 * time.Second

const walBatchMax = 64

func walList(out string, now time.Time) (files []string, pending, ready int64) {
	size := map[string]int64{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) != 3 || !walName.MatchString(f[0]) {
			continue
		}
		mtime, err1 := strconv.ParseInt(f[1], 10, 64)
		n, err2 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		pending += n
		if now.Sub(time.Unix(mtime, 0)) >= walQuiet {
			files, size[f[0]] = append(files, f[0]), n
		}
	}
	slices.Sort(files)
	files = files[:min(len(files), walBatchMax)]
	for _, f := range files {
		ready += size[f]
	}
	return files, pending, ready
}

func walHelper(volume string, readOnly bool, cmd ...string) runtime.ContainerSpec {
	return runtime.ContainerSpec{Image: "alpine:3", Cmd: cmd, Labels: map[string]string{"yawl.backup": "1"},
		Mounts: []runtime.Mount{{Source: volume, Target: "/data", Volume: true, ReadOnly: readOnly}}}
}

const walIn = "/data/yawl_wal"

func (d *Dispatcher) shipWAL(ctx context.Context, a proto.ShipWALArgs) proto.Result {
	if d.Keys != nil {
		d.Keys.Masker().Add(a.Storage.SecretKey)
	}
	rec, err := seal.ParsePublicKey(a.RecoveryPubkey)
	if err != nil {
		return proto.Result{Cause: "bad recovery key: " + err.Error()}
	}
	_, volume, _, _ := dbNames("", a.Project, a.Env, a.Name, 0)
	var gone []string
	for _, f := range a.Remove {
		if walName.MatchString(f) {
			gone = append(gone, walIn+"/"+f)
		}
	}
	if len(gone) > 0 {
		if exit, stderr, err := d.rt.RunOnce(ctx, walHelper(volume, false, append([]string{"rm", "-f", "--"}, gone...)...), nil, nil); err != nil || exit != 0 {
			return proto.Result{Cause: fmt.Sprintf("removing shipped WAL of %s: exit %d %s %v", a.Name, exit, stderr, err)}
		}
	}
	var out strings.Builder
	list := walHelper(volume, true, "sh", "-c", `cd `+walIn+` 2>/dev/null || exit 0; for f in *; do [ -f "$f" ] && stat -c "%n %Y %s" "$f"; done; exit 0`)
	if exit, stderr, err := d.rt.RunOnce(ctx, list, nil, &out); err != nil || exit != 0 {
		return proto.Result{Cause: fmt.Sprintf("listing the WAL of %s: exit %d %s %v", a.Name, exit, stderr, err)}
	}
	files, pending, ready := walList(out.String(), time.Now())
	data := proto.ShipWALData{Pending: pending}
	segments := slices.DeleteFunc(slices.Clone(files), func(f string) bool { return len(f) != 24 })
	if len(files) == 0 {
		return ok(data)
	}
	first, last := files[0], files[len(files)-1]
	if len(segments) > 0 {
		first, last = segments[0], segments[len(segments)-1]
	}
	key := a.KeyPrefix + first + ".tar.gz.age"
	tar := walHelper(volume, true, append([]string{"tar", "-C", walIn, "-czf", "-"}, files...)...)
	size, sum, err := backup.DumpSpec(ctx, d.rt, tar, rec, target(a.Storage), key)
	if err != nil {
		b, _ := json.Marshal(data)
		return proto.Result{Cause: err.Error(), Data: b}
	}
	data.Pending = pending - ready
	data.Batch = &proto.WALBatch{Key: key, First: first, Last: last, Size: size, SHA256: sum, Files: files}
	return ok(data)
}
