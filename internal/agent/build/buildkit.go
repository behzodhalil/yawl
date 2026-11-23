package build

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/moby/client"
	"github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"

	"github.com/behzodhalil/yawl/internal/agent/secrets"
)

const tailLines, tailBytes, logBufBytes = 30, 4 << 10, 64 << 10

// buildLog keeps the last logBufBytes of build output as raw bytes, so a
// secret split across BuildKit log chunks is whole again before masking.
type buildLog struct {
	buf []byte
	cut bool
}

func (l *buildLog) Write(p []byte) {
	l.buf = append(l.buf, p...)
	if over := len(l.buf) - logBufBytes; over > 0 {
		l.buf, l.cut = l.buf[over:], true
	}
}

func (l *buildLog) cause(err error, r Request) string {
	var m secrets.Masker
	m.Add(r.Token)
	for _, v := range r.Secrets {
		m.Add(string(v))
	}
	head := m.Mask("build failed: " + err.Error())
	lines := strings.Split(strings.TrimRight(m.Mask(string(l.buf)), "\n"), "\n")
	if l.cut {
		lines = lines[1:]
	}
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	body := strings.Join(lines, "\n")
	if room := tailBytes - len(head) - 1; len(body) > room {
		body = body[len(body)-max(room, 0):]
		if i := strings.IndexByte(body, '\n'); i >= 0 {
			body = body[i+1:]
		}
	}
	if strings.TrimSpace(body) == "" {
		return head
	}
	return head + "\n" + body
}

func connect(ctx context.Context, c *client.Client) (*bk.Client, error) {
	b, err := bk.New(ctx, "",
		bk.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return c.DialHijack(ctx, "/grpc", "h2c", nil)
		}),
		bk.WithSessionDialer(func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
			return c.DialHijack(ctx, "/session", proto, meta)
		}))
	if err == nil {
		_, err = b.ListWorkers(ctx)
	}
	if err != nil { // 28: the generated Dockerfile's secret env= mounts need Dockerfile frontend 1.10
		return nil, fmt.Errorf("Docker's BuildKit is not reachable (%v). Yawl needs Docker 28 or newer.", err)
	}
	return b, nil
}

func solve(ctx context.Context, c *client.Client, contextDir, dockerfileDir string, r Request, sha string) error {
	b, err := connect(ctx, c)
	if err != nil {
		return err
	}
	defer b.Close()
	ctxFS, err := contextFS(contextDir, dockerfileDir != contextDir)
	if err != nil {
		return err
	}
	dfFS, err := fsutil.NewFS(dockerfileDir)
	if err != nil {
		return err
	}
	return runSolve(ctx, b, nil, bk.SolveOpt{
		Frontend:      "dockerfile.v0",
		FrontendAttrs: dockerfileAttrs(sha, r.NoCache),
		LocalMounts:   map[string]fsutil.FS{"context": ctxFS, "dockerfile": dfFS},
		Session:       []session.Attachable{secretsprovider.FromMap(r.Secrets)},
		Exports:       []bk.ExportEntry{{Type: "moby", Attrs: map[string]string{"name": r.Image}}},
	}, r)
}

// contextFS is the repository BuildKit reads. A Dockerfile Yawl wrote copies
// all of it, so .git stays out of the image; the frontend
// still applies the repository's own .dockerignore.
func contextFS(dir string, generated bool) (fsutil.FS, error) {
	fs, err := fsutil.NewFS(dir)
	if err != nil || !generated {
		return fs, err
	}
	return fsutil.NewFilterFS(fs, &fsutil.FilterOpt{ExcludePatterns: []string{".git"}})
}

func runSolve(ctx context.Context, b *bk.Client, def *llb.Definition, opt bk.SolveOpt, r Request) error {
	ch := make(chan *bk.SolveStatus)
	log := &buildLog{}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		_, err := b.Solve(gctx, def, opt, ch)
		return err
	})
	g.Go(func() error {
		for s := range ch {
			for _, l := range s.Logs {
				log.Write(l.Data)
			}
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return errors.New(log.cause(err, r))
	}
	return nil
}

func dockerfileAttrs(sha string, noCache bool) map[string]string {
	return withNoCache(map[string]string{"label:yawl.commit": sha}, noCache)
}

func withNoCache(attrs map[string]string, noCache bool) map[string]string {
	if noCache {
		attrs["no-cache"] = ""
	}
	return attrs
}
