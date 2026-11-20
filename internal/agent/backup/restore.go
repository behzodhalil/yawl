package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"filippo.io/age"
	"github.com/minio/minio-go/v7"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
)

func Restore(ctx context.Context, rt runtime.Runtime, db DB, id *age.X25519Identity, t Target, key, sum string) error {
	return restoreWith(ctx, rt, restoreSpec(db), id, t, key, sum)
}

var SpoolDir string

// verified copies r to an unlinked file and returns it rewound only when its
// SHA-256 is sum, the one recorded at backup. Anyone who can write the bucket
// can encrypt to the public recovery key, so an object swapped there must not
// reach age, a loader or tar: a dump runs as the database's superuser.
func verified(r io.Reader, sum string) (*os.File, error) {
	if sum == "" {
		return nil, errors.New("no SHA-256 was recorded for it, so it cannot be checked; nothing was restored")
	}
	if SpoolDir != "" {
		if err := os.MkdirAll(SpoolDir, 0o700); err != nil {
			return nil, err
		}
	}
	f, err := os.CreateTemp(SpoolDir, "restore-*")
	if err != nil {
		return nil, err
	}
	os.Remove(f.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), r); err != nil {
		f.Close()
		return nil, fmt.Errorf("download: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		f.Close()
		return nil, fmt.Errorf("its SHA-256 is %s, not %s as recorded at backup: it was changed in storage; nothing was restored", got, sum)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func restoreSpec(db DB) runtime.ContainerSpec {
	return runtime.ContainerSpec{Image: db.Image, Network: db.Network, Env: []string{"PGPASSWORD=" + db.Password},
		Cmd: []string{"pg_restore", "--no-owner", "--no-privileges", "--exit-on-error", "--single-transaction", "-h", db.Host, "-U", "app", "-d", "app"}}
}

func volumeHelper(volume, script string) runtime.ContainerSpec {
	return runtime.ContainerSpec{Image: "alpine:3", Mounts: []runtime.Mount{{Source: volume, Target: "/data", Volume: true}},
		Cmd: []string{"sh", "-c", script}, Labels: map[string]string{"yawl.backup": "1"}}
}

func VolumeStageSpec(volume string) runtime.ContainerSpec {
	return volumeHelper(volume, "rm -rf /data/.yawl-restore && mkdir /data/.yawl-restore && tar -C /data/.yawl-restore -xf -")
}

func volumeSwapSpec(volume string) runtime.ContainerSpec {
	return volumeHelper(volume, "find /data -mindepth 1 -maxdepth 1 ! -name .yawl-restore -exec rm -rf {} + && "+
		"find /data/.yawl-restore -mindepth 1 -maxdepth 1 -exec mv {} /data/ \\; && rmdir /data/.yawl-restore")
}

func RestoreVolume(ctx context.Context, rt runtime.Runtime, volume string, id *age.X25519Identity, t Target, key, sum string) error {
	c, err := t.client()
	if err != nil {
		return err
	}
	obj, err := c.GetObject(ctx, t.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("fetch %s: %w", key, err)
	}
	defer obj.Close()
	if err := restoreVolumeStream(ctx, rt, volume, obj, id, sum); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

func restoreVolumeStream(ctx context.Context, rt runtime.Runtime, volume string, r io.Reader, id *age.X25519Identity, sum string) error {
	f, err := verified(r, sum)
	if err != nil {
		return err
	}
	defer f.Close()
	plain, err := age.Decrypt(f, id)
	if err != nil {
		return fmt.Errorf("decrypt (is it the right recovery key?): %w", err)
	}
	in := &firstErr{r: plain}
	err = run(ctx, rt, VolumeStageSpec(volume), in)
	if _, derr := io.Copy(io.Discard, in); err == nil && derr != nil {
		err = derr
	}
	if err == nil {
		err = in.err
	}
	if err != nil {
		run(context.WithoutCancel(ctx), rt, volumeHelper(volume, "rm -rf /data/.yawl-restore"), nil)
		return fmt.Errorf("the backup did not read whole, the volume is unchanged: %w", err)
	}
	return run(ctx, rt, volumeSwapSpec(volume), nil)
}

// firstErr remembers the first read error, which a container reading stdin may swallow.
type firstErr struct {
	r   io.Reader
	err error
}

func (f *firstErr) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if err != nil && err != io.EOF && f.err == nil {
		f.err = err
	}
	return n, err
}

func run(ctx context.Context, rt runtime.Runtime, spec runtime.ContainerSpec, stdin io.Reader) error {
	exit, stderr, err := rt.RunOnce(ctx, spec, stdin, nil)
	if err == nil && exit != 0 {
		err = fmt.Errorf("%s exited %d: %s", spec.Cmd[0], exit, stderr)
	}
	return err
}

func RestoreSpec(ctx context.Context, rt runtime.Runtime, spec runtime.ContainerSpec, id *age.X25519Identity, t Target, key, sum string) error {
	return restoreWith(ctx, rt, spec, id, t, key, sum)
}

// RestoreTarsMany restores several tar.gz objects, WAL batches, with one
// helper, not one each, which took hours for a week of batches (Plan 31 M4).
// Each is decrypted and its entries copied into one plain tar on spec's stdin:
// busybox tar, in the helper image, stops at the first archive's end and has
// no --ignore-zeros. sums are the keys' SHA-256s, in order.
func RestoreTarsMany(ctx context.Context, rt runtime.Runtime, spec runtime.ContainerSpec, id *age.X25519Identity, t Target, keys, sums []string) error {
	if len(sums) != len(keys) {
		return errors.New("the server sent no SHA-256 for some WAL batches; update the server")
	}
	c, err := t.client()
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	defer pr.Close()
	go func() {
		tw := tar.NewWriter(pw)
		for i, key := range keys {
			if err := copyTarGz(ctx, c, t.Bucket, key, sums[i], id, tw); err != nil {
				pw.CloseWithError(fmt.Errorf("%s: %w", key, err))
				return
			}
		}
		pw.CloseWithError(tw.Close())
	}()
	in := &firstErr{r: pr}
	exit, stderr, err := rt.RunOnce(ctx, spec, in, nil)
	if err == nil && in.err != nil {
		err = fmt.Errorf("reading the backup: %w", in.err)
	}
	if err == nil && exit != 0 {
		err = fmt.Errorf("%s exited %d: %s", spec.Cmd[0], exit, stderr)
	}
	return err
}

func copyTarGz(ctx context.Context, c *minio.Client, bucket, key, sum string, id *age.X25519Identity, tw *tar.Writer) error {
	obj, err := c.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer obj.Close()
	f, err := verified(obj, sum)
	if err != nil {
		return err
	}
	defer f.Close()
	plain, err := age.Decrypt(f, id)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(plain)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
}

func restoreWith(ctx context.Context, rt runtime.Runtime, spec runtime.ContainerSpec, id *age.X25519Identity, t Target, key, sum string) error {
	c, err := t.client()
	if err != nil {
		return err
	}
	obj, err := c.GetObject(ctx, t.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("fetch %s: %w", key, err)
	}
	defer obj.Close()
	if err := restoreStream(ctx, rt, spec, obj, id, sum); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

func restoreStream(ctx context.Context, rt runtime.Runtime, spec runtime.ContainerSpec, r io.Reader, id *age.X25519Identity, sum string) error {
	f, err := verified(r, sum)
	if err != nil {
		return err
	}
	defer f.Close()
	plain, err := age.Decrypt(f, id)
	if err != nil {
		return fmt.Errorf("decrypt (is it the right recovery key?): %w", err)
	}
	in := &firstErr{r: plain} // a cut backup must fail: plain SQL can end cleanly at any statement
	exit, stderr, err := rt.RunOnce(ctx, spec, in, nil)
	if err == nil && in.err != nil {
		err = fmt.Errorf("reading the backup: %w", in.err)
	}
	if err == nil && exit != 0 {
		err = fmt.Errorf("%s exited %d: %s", spec.Cmd[0], exit, stderr)
	}
	return err
}
