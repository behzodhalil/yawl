package exec

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

var importName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var importClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: guarded.DialContext, ResponseHeaderTimeout: time.Minute}}

func (d *Dispatcher) importDump(ctx context.Context, a proto.ImportArgs) proto.Result {
	if a.Kind == "valkey" {
		return proto.Result{Cause: "Valkey is a cache; Yawl does not import into it."}
	}
	if a.Kind == "clickhouse" {
		return proto.Result{Cause: "Yawl does not import into ClickHouse; load your data with clickhouse-client."}
	}
	pw, cause := d.open(proto.SealedVar{Key: "PASSWORD", Box: a.Password})
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	opened, err := d.importSource(ctx, a)
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	defer opened.Close()
	src := &readErr{r: opened}
	in := bufio.NewReaderSize(src, 64<<10)
	var r io.Reader = in
	if head, _ := in.Peek(2); len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		z, err := gzip.NewReader(in)
		if err != nil {
			return proto.Result{Cause: "the file looks gzipped but is not: " + err.Error()}
		}
		r = z
	}
	body := bufio.NewReaderSize(r, 64<<10)
	if _, err := body.Peek(1); err == io.EOF && src.err == nil {
		return proto.Result{Cause: "the file is empty"}
	}
	_, _, network, image := dbNames(a.Kind, a.Project, a.Env, a.Name, a.Version)
	var spec = clientSpec(engine{}, image, network, a.Name, string(pw), nil)
	if e, found := engines[a.Kind]; found {
		spec = clientSpec(e, image, network, a.Name, string(pw), e.load(a.Name))
		if a.Kind == "mongodb" { // the source's own database name would leave the data beside app, where the URL never looks (Plan 45 M2)
			spec.Cmd = mongoTool(`mongorestore --archive --drop --nsExclude 'admin.*' --nsExclude 'config.*' --nsFrom '$db$.$coll$' --nsTo 'app.$coll$'`)
		}
	} else { // Postgres: a custom-format dump starts PGDMP; anything else is SQL
		spec.Env = []string{"PGPASSWORD=" + string(pw)}
		if head, _ := body.Peek(5); string(head) == "PGDMP" {
			spec.Cmd = []string{"pg_restore", "--no-owner", "--no-privileges", "--exit-on-error", "--single-transaction", "-h", a.Name, "-U", "app", "-d", "app"}
		} else {
			spec.Cmd = []string{"psql", "-v", "ON_ERROR_STOP=1", "--single-transaction", "-q", "-h", a.Name, "-U", "app", "-d", "app"}
		}
	}
	n := &counter{w: io.Discard}
	exit, stderr, err := d.rt.RunOnce(ctx, spec, io.TeeReader(body, n), io.Discard)
	if src.err != nil {
		return proto.Result{Cause: "the dump was cut off: " + src.err.Error()}
	}
	if err != nil {
		return proto.Result{Cause: err.Error()}
	}
	if exit != 0 {
		return proto.Result{Cause: fmt.Sprintf("%s stopped (exit %d): %s%s", spec.Cmd[0], exit, strings.TrimSpace(stderr), importHint(stderr))}
	}
	return proto.Result{OK: true, Output: fmt.Sprintf("Loaded %d MB with %s.", n.n>>20, spec.Cmd[0])}
}

// importSource opens the link or the file in the imports folder. An error
// names the link's host and path only: a presigned query is a secret (D6).
func (d *Dispatcher) importSource(ctx context.Context, a proto.ImportArgs) (io.ReadCloser, error) {
	if a.URL != "" {
		u, err := url.Parse(a.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, errors.New("the link must be http or https")
		}
		shown := u.Host + u.Path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
		if err != nil {
			return nil, errors.New("the link is not a URL")
		}
		res, err := importClient.Do(req)
		if err != nil {
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			return nil, fmt.Errorf("fetching %s: %v", shown, err)
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return nil, fmt.Errorf("fetching %s: %s", shown, res.Status)
		}
		return res.Body, nil
	}
	if !importName.MatchString(a.Path) || d.ImportDir == "" {
		return nil, fmt.Errorf("%q: name a file in %s, without folders", a.Path, d.ImportDir)
	}
	f, err := os.Open(filepath.Join(d.ImportDir, a.Path))
	if err != nil {
		return nil, fmt.Errorf("no file %s in %s", a.Path, d.ImportDir)
	}
	return f, nil
}

type readErr struct {
	r   io.Reader
	err error
}

func (e *readErr) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err != nil && err != io.EOF && e.err == nil {
		e.err = err
	}
	return n, err
}

func importHint(stderr string) string {
	switch {
	case strings.Contains(stderr, "role ") && strings.Contains(stderr, "does not exist"):
		return " Make the dump with pg_dump --no-owner --no-acl."
	case strings.Contains(stderr, "ERROR 1227"), strings.Contains(stderr, "ERROR 1419"), strings.Contains(stderr, "DEFINER"):
		return " Make the dump with mysqldump --set-gtid-purged=OFF, and without DEFINER clauses."
	}
	return ""
}
