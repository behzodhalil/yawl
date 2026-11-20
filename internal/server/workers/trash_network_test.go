package workers

import (
	"context"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
)

func TestEnvironmentPurgeSendsNoNetworkRemoval(t *testing.T) {
	st, w, gw, a, now := trashSetup(t, "web")
	if err := a.Delete(context.Background(), "environment", "env_1", "owner"); err != nil {
		t.Fatal(err)
	}
	w.Tick(context.Background())
	*now = tDel.Add(49 * time.Hour)
	w.Tick(context.Background())
	var envs int
	st.DB.QueryRow(`SELECT count(*) FROM environments WHERE id = 'env_1'`).Scan(&envs)
	if envs != 0 {
		t.Fatal("not purged")
	}
	known := map[string]bool{proto.CmdRemoveRoute: true, proto.CmdStop: true, proto.CmdDrain: true, proto.CmdRemoveVolume: true, proto.CmdDeleteBackups: true, proto.CmdCronRemove: true}
	for _, c := range gw.sent {
		if !known[c.Name] {
			t.Fatalf("the purge sent %s: %q", c.Name, sentArgs(gw))
		}
	}
	if len(gw.sent) == 0 {
		t.Fatal("nothing sent")
	}
}
