//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"
)

func TestE2ETrash(t *testing.T) {
	b := setup(t)
	b.withSecret()
	b.commit("env", "v1")
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if v, _ := get(); v != planted {
		t.Fatalf("before: %q", v)
	}
	del := func() {
		if code := b.apiBody("DELETE", "/api/services/e2e/production/hello", map[string]string{"confirm": "hello"}, nil); code != 204 {
			t.Fatalf("delete: %d", code)
		}
	}
	del()
	waitFor(t, 90*time.Second, "the deleted service to stop answering", func() bool { v, _ := get(); return v != planted })
	var items []struct{ Kind, ID string }
	b.api("GET", "/api/trash", &items)
	if len(items) != 1 {
		t.Fatalf("trash %+v", items)
	}
	if code := b.api("POST", "/api/trash/service/"+items[0].ID+"/restore", nil); code != 202 {
		t.Fatalf("restore: %d", code)
	}
	waitFor(t, 120*time.Second, "the restored service to answer with its variable", func() bool { v, _ := get(); return v == planted })
	del()
	b.store().DB.Exec(`UPDATE services SET purge_at = 1 WHERE name = 'hello'`)
	c, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 150*time.Second, "the purged service's containers to be gone", func() bool {
		res, err := c.ContainerList(context.Background(), dockerclient.ContainerListOptions{All: true,
			Filters: make(dockerclient.Filters).Add("label", "yawl.service=hello").Add("label", "yawl.project=e2e")})
		if err != nil {
			return false
		}
		var n int
		b.store().DB.QueryRow(`SELECT count(*) FROM services WHERE name = 'hello'`).Scan(&n)
		return len(res.Items) == 0 && n == 0
	})
}
