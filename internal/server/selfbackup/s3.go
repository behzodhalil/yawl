package selfbackup

import (
	"context"
	"io"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/behzodhalil/yawl/internal/server/egress"
)

const Prefix = "yawl-server/"

type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	List(ctx context.Context, prefix string) ([]string, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

type s3 struct {
	c      *minio.Client
	bucket string
}

func S3(endpoint, bucket, region, access, secret string, insecure bool) (Store, error) {
	if strings.HasPrefix(endpoint, "http://") {
		insecure = true
	}
	endpoint = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://"), "/")
	opts := &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: !insecure, Region: region}
	if egress.Control != nil { // minio dials with its own transport, not http.DefaultTransport
		tr, err := minio.DefaultTransport(!insecure)
		if err != nil {
			return nil, err
		}
		tr.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: egress.Control}).DialContext
		opts.Transport = tr
	}
	c, err := minio.New(endpoint, opts)
	return &s3{c: c, bucket: bucket}, err
}

func (s *s3) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	_, err := s.c.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: "application/octet-stream", PartSize: 16 << 20})
	return err
}

func (s *s3) List(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	for o := range s.c.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		out = append(out, o.Key)
	}
	sort.Strings(out)
	return out, nil
}

func (s *s3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	o, err := s.c.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := o.Stat(); err != nil { // GetObject is lazy: a missing key shows only here
		o.Close()
		return nil, err
	}
	return o, nil
}

func (s *s3) Delete(ctx context.Context, key string) error {
	return s.c.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

type Mem struct {
	Objects map[string][]byte
	PutErr  error
}

func (m *Mem) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	if m.PutErr != nil {
		return m.PutErr
	}
	b, err := io.ReadAll(r)
	if m.Objects == nil {
		m.Objects = map[string][]byte{}
	}
	m.Objects[key] = b
	return err
}

func (m *Mem) List(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for k := range m.Objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *Mem) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := m.Objects[key]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func (m *Mem) Delete(_ context.Context, key string) error {
	delete(m.Objects, key)
	return nil
}
