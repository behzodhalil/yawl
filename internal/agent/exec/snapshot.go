package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

const keepSnapshots = 3

func createFile(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
}

func (d *Dispatcher) snapshot(ctx context.Context, key string, a proto.SnapshotArgs) proto.Result {
	pw, cause := d.open(proto.SealedVar{Key: "PASSWORD", Box: a.Password})
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	container, _, network, image := dbNames(a.Kind, a.Project, a.Env, a.Name, a.Version)
	dir := filepath.Join(d.SnapDir, container)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	id, _, _ := cutDeploy(key)
	path := filepath.Join(dir, id+".dump")
	create := d.SnapFile
	if create == nil {
		create = createFile
	}
	f, err := create(path)
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	n := &counter{w: f}
	spec := runtime.ContainerSpec{Image: image, Network: network, Env: []string{"PGPASSWORD=" + string(pw)},
		Cmd: []string{"pg_dump", "--format=custom", "-h", a.Name, "-U", "app", "app"}}
	if e, found := engines[a.Kind]; found && e.dump != nil {
		spec = clientSpec(e, image, network, a.Name, string(pw), e.dump(a.Name))
	}
	exit, stderr, err := d.rt.RunOnce(ctx, spec, nil, n)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && exit != 0 {
		err = fmt.Errorf("%s exited %d: %s", spec.Cmd[0], exit, stderr)
	}
	if err != nil {
		os.Remove(path)
		if errors.Is(err, syscall.ENOSPC) {
			return proto.Result{Cause: fmt.Sprintf("Not enough free disk for a snapshot of %s (%d MB free).", a.Name, freeMB(dir))}
		}
		return proto.Result{Cause: "snapshot of " + a.Name + ": " + err.Error()}
	}
	prune(dir, keepSnapshots)
	return ok(proto.SnapshotData{Path: path, Size: n.n})
}

func cutDeploy(key string) (string, string, bool) {
	for i := range key {
		if key[i] == ':' {
			return key[:i], key[i+1:], true
		}
	}
	return key, "", false
}

func prune(dir string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		mod  int64
	}
	var fs []file
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
			fs = append(fs, file{e.Name(), fi.ModTime().UnixNano()})
		}
	}
	slices.SortFunc(fs, func(a, b file) int { return int(b.mod - a.mod) })
	for i := keep; i < len(fs); i++ {
		os.Remove(filepath.Join(dir, fs[i].name))
	}
}

func freeMB(dir string) uint64 {
	var s syscall.Statfs_t
	if syscall.Statfs(dir, &s) != nil {
		return 0
	}
	return s.Bavail * uint64(s.Bsize) >> 20
}

type counter struct {
	w io.Writer
	n int64
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
