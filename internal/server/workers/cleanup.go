package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type Cleanup struct {
	St  *store.Store
	GW  Sender
	Now func() time.Time
}

type CleanupWhy string

const (
	CleanupDisk    CleanupWhy = "disk"
	CleanupNightly CleanupWhy = "nightly"
	CleanupByHand  CleanupWhy = "by hand"
)

type CleanupRun struct {
	At         int64      `json:"at"`
	Why        CleanupWhy `json:"why"`
	Containers int        `json:"containers,omitempty"`
	Images     int        `json:"images,omitempty"`
	Freed      int64      `json:"freed"`
	Cause      string     `json:"cause,omitempty"`
}

func (w *Cleanup) Tick(ctx context.Context) error {
	if app.Adopting(ctx, w.St.Q) {
		return nil
	}
	servers, err := w.St.Q.ListServers(ctx)
	if err != nil {
		return err
	}
	now := w.Now()
	var wg sync.WaitGroup
	for _, s := range servers {
		if s.Offline == 1 {
			continue
		}
		last, _ := strconv.ParseInt(setting(ctx, w.St.Q, "cleanup.last."+s.ID), 10, 64)
		since := now.Sub(time.UnixMilli(last))
		switch {
		case s.DiskUsedPct >= 80 && since >= time.Hour:
			wg.Go(func() { RunCleanup(ctx, w.St.Q, w.GW, s.ID, s.DiskUsedPct >= 90, CleanupDisk, now) })
		case now.UTC().Hour() == 4 && setting(ctx, w.St.Q, "cleanup.night."+s.ID) != now.UTC().Format("2006-01-02"):
			w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "cleanup.night." + s.ID, Value: now.UTC().Format("2006-01-02")})
			wg.Go(func() { RunCleanup(ctx, w.St.Q, w.GW, s.ID, false, CleanupNightly, now) })
		}
	}
	wg.Wait()
	return nil
}

var cleaning sync.Map

var ErrCleaning = errors.New("a cleanup is running on this server; its result shows here when it ends")

func RunCleanup(ctx context.Context, q *db.Queries, gw Sender, serverID string, allCache bool, why CleanupWhy, now time.Time) (CleanupRun, error) {
	if _, busy := cleaning.LoadOrStore(serverID, true); busy {
		return CleanupRun{}, ErrCleaning
	}
	defer cleaning.Delete(serverID)
	cmd, err := proto.NewCommand(fmt.Sprintf("cleanup:%s:%d", serverID, now.UnixMilli()), proto.CmdCleanup, proto.CleanupArgs{AllCache: allCache})
	if err != nil {
		return CleanupRun{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	res, err := gw.Send(cctx, serverID, cmd)
	cancel()
	run := CleanupRun{At: now.UnixMilli(), Why: why}
	switch {
	case err != nil:
		run.Cause = err.Error()
	case !res.OK:
		run.Cause = res.Cause
	default:
		var d proto.CleanupData
		json.Unmarshal(res.Data, &d)
		run.Containers, run.Images, run.Freed = d.Containers, d.Images, d.Freed
	}
	b, _ := json.Marshal(run)
	q.SetSetting(ctx, db.SetSettingParams{Key: "cleanup.last." + serverID, Value: strconv.FormatInt(run.At, 10)})
	q.SetSetting(ctx, db.SetSettingParams{Key: "cleanup.result." + serverID, Value: string(b)})
	if run.Cause != "" {
		return run, fmt.Errorf("%s", run.Cause)
	}
	return run, nil
}

func LastCleanup(ctx context.Context, q *db.Queries, serverID string) (CleanupRun, error) {
	var run CleanupRun
	v, err := q.GetSetting(ctx, "cleanup.result."+serverID)
	if err != nil {
		return run, nil
	}
	return run, json.Unmarshal([]byte(v), &run)
}
