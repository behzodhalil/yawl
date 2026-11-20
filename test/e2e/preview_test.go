//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"
)

func getAt(h string) (string, int) {
	req, _ := http.NewRequest("GET", "http://127.0.0.1/", nil)
	req.Host = h
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err.Error(), 0
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return strings.TrimSpace(string(b)), res.StatusCode
}

func TestE2EPreview(t *testing.T) {
	b := setup(t)
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if code := b.apiBody("PUT", "/api/environments/e2e/production/preview", map[string]bool{"on": true}, nil); code != 204 {
		t.Fatalf("preview switch: %d", code)
	}
	st := b.store()
	if _, err := st.DB.Exec(`INSERT INTO previews (id, project_id, source_env_id, number, repo, title, branch, state, last_push_at, created_at, updated_at)
		SELECT 'pre_e2e', e.project_id, e.id, 12, 'e2e/hello', 'Faster search', 'master', 'waiting', ?, ?, ? FROM environments e WHERE e.name = 'production'`,
		time.Now().UnixMilli(), time.Now().UnixMilli(), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	const prHost = "hello-pr-12.127-0-0-1.sslip.io"
	waitFor(t, 4*time.Minute, "pr-12 answering at its own URL", func() bool { v, code := getAt(prHost); return code == 200 && v == "v1" })
	if v, _ := get(); v != "v1" {
		t.Fatalf("production after the preview: %q", v)
	}
	var previews []struct{ ID, State, Env string }
	b.api("GET", "/api/projects/e2e/previews", &previews)
	if len(previews) != 1 || previews[0].State != "live" || previews[0].Env != "pr-12" {
		t.Fatalf("%+v", previews)
	}
	if code := b.api("POST", "/api/previews/pre_e2e/close", nil); code != 204 {
		t.Fatalf("close: %d", code)
	}
	c, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	state := ""
	deadline := time.Now().Add(4 * time.Minute)
	for {
		res, err := c.ContainerList(context.Background(), dockerclient.ContainerListOptions{All: true,
			Filters: make(dockerclient.Filters).Add("label", "yawl.env=pr-12").Add("label", "yawl.project=e2e")})
		var envs, svcs, stopped int
		var deleted sql.NullInt64
		st.DB.QueryRow(`SELECT count(*) FROM environments WHERE name = 'pr-12'`).Scan(&envs)
		st.DB.QueryRow(`SELECT count(*), COALESCE(SUM(s.stopped), 0), MIN(s.deleted_at) FROM services s JOIN environments e ON e.id = s.environment_id WHERE e.name = 'pr-12'`).Scan(&svcs, &stopped, &deleted)
		body, code := getAt(prHost)
		state = fmt.Sprintf("containers %d (err %v), environment rows %d, services %d (stopped %d, deleted_at %v), URL %d %.40q", len(res.Items), err, envs, svcs, stopped, deleted, code, body)
		if err == nil && len(res.Items) == 0 && envs == 0 && body != "v1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pr-12 not gone after 4 minutes: %s", state)
		}
		time.Sleep(3 * time.Second)
	}
	if v, _ := get(); v != "v1" {
		t.Fatalf("production after the preview closed: %q", v)
	}
}
