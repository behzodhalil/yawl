package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/seal"
)

type Target struct {
	Endpoint, Bucket, Region, AccessKey, SecretKey string
	Insecure                                       bool
}

type DB struct{ Image, Network, Host, Password string }

var errUpload = errors.New("upload stopped")

// partSize bounds memory: minio-go buffers one part per upload, and for an
// unknown size its default part is hundreds of MB.
const partSize = 16 << 20

func (t Target) client() (*minio.Client, error) {
	return minio.New(t.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(t.AccessKey, t.SecretKey, ""), Secure: !t.Insecure, Region: t.Region})
}

func Dump(ctx context.Context, rt runtime.Runtime, db DB, to seal.PublicKey, t Target, key string) (int64, string, error) {
	return dumpWith(ctx, rt, runtime.ContainerSpec{Image: db.Image, Network: db.Network, Env: []string{"PGPASSWORD=" + db.Password},
		Cmd: []string{"pg_dump", "--format=custom", "-h", db.Host, "-U", "app", "app"}}, to, t, key)
}

func VolumeDumpSpec(volume string) runtime.ContainerSpec {
	return runtime.ContainerSpec{Image: "alpine:3", Mounts: []runtime.Mount{{Source: volume, Target: "/data", Volume: true, ReadOnly: true}},
		Cmd: []string{"tar", "-C", "/data", "-cf", "-", "."}, Labels: map[string]string{"yawl.backup": "1"}}
}

func DumpVolume(ctx context.Context, rt runtime.Runtime, volume string, to seal.PublicKey, t Target, key string) (int64, string, error) {
	return dumpWith(ctx, rt, VolumeDumpSpec(volume), to, t, key)
}

func DumpSpec(ctx context.Context, rt runtime.Runtime, spec runtime.ContainerSpec, to seal.PublicKey, t Target, key string) (int64, string, error) {
	return dumpWith(ctx, rt, spec, to, t, key)
}

func dumpWith(ctx context.Context, rt runtime.Runtime, spec runtime.ContainerSpec, to seal.PublicKey, t Target, key string) (int64, string, error) {
	c, err := t.client()
	if err != nil {
		return 0, "", err
	}
	r, err := Recipient(to)
	if err != nil {
		return 0, "", err
	}
	pr, pw := io.Pipe()
	put := make(chan error, 1)
	go func() {
		_, err := c.PutObject(ctx, t.Bucket, key, pr, -1, minio.PutObjectOptions{ContentType: "application/octet-stream", PartSize: partSize})
		if err != nil {
			pr.CloseWithError(fmt.Errorf("%w: %v", errUpload, err))
		}
		put <- err
	}()
	fail := func(err error, dumpFailed bool) (int64, string, error) {
		pw.CloseWithError(err)
		putErr := <-put
		c.RemoveObject(context.WithoutCancel(ctx), t.Bucket, key, minio.RemoveObjectOptions{})
		if putErr != nil && !dumpFailed {
			return 0, "", fmt.Errorf("upload to %s: %w", t.Bucket, putErr)
		}
		return 0, "", err
	}
	h, n := sha256.New(), &counter{}
	enc, err := age.Encrypt(io.MultiWriter(pw, h, n), r)
	if err != nil {
		return fail(err, false)
	}
	exit, stderr, err := rt.RunOnce(ctx, spec, nil, enc)
	if err == nil && exit != 0 {
		err = fmt.Errorf("%s exited %d: %s", spec.Cmd[0], exit, stderr)
	}
	if err != nil {
		return fail(err, !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, errUpload))
	}
	if err := enc.Close(); err != nil {
		return fail(err, false)
	}
	pw.Close()
	if err := <-put; err != nil {
		return 0, "", fmt.Errorf("upload to %s: %w", t.Bucket, err)
	}
	return n.n, hex.EncodeToString(h.Sum(nil)), nil
}

func Delete(ctx context.Context, t Target, keys []string) ([]string, error) {
	c, err := t.client()
	if err != nil {
		return nil, err
	}
	var gone []string
	var errs []error
	for _, k := range keys {
		if err := c.RemoveObject(ctx, t.Bucket, k, minio.RemoveObjectOptions{}); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", k, err))
			continue
		}
		gone = append(gone, k)
	}
	return gone, errors.Join(errs...)
}

type counter struct{ n int64 }

func (c *counter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

func Copy(ctx context.Context, from, to Target, key string) (int64, error) {
	src, err := from.client()
	if err != nil {
		return 0, err
	}
	dst, err := to.client()
	if err != nil {
		return 0, err
	}
	st, err := src.StatObject(ctx, from.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", key, err)
	}
	obj, err := src.GetObject(ctx, from.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", key, err)
	}
	defer obj.Close()
	info, err := dst.PutObject(ctx, to.Bucket, key, obj, st.Size, minio.PutObjectOptions{PartSize: partSize})
	if err != nil {
		return 0, fmt.Errorf("write %s to %s: %w", key, to.Bucket, err)
	}
	return info.Size, nil
}
