package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

type PITR struct {
	St  *store.Store
	GW  Sender
	V   *vault.Vault
	Now func() time.Time
}

const walLagLimit = 2 << 30

func (w *PITR) Tick(ctx context.Context) error {
	if app.Adopting(ctx, w.St.Q) {
		return nil
	}
	dbs, err := w.St.Q.ListPITRServices(ctx)
	if err != nil || len(dbs) == 0 {
		return err
	}
	storage, err := storageFrom(ctx, w.St.Q, w.V)
	if err != nil {
		return nil
	}
	rec, err := w.St.Q.GetSetting(ctx, "recovery.pubkey")
	if err != nil {
		return nil
	}
	for _, d := range dbs {
		if err := w.ship(ctx, d, storage, rec); err != nil {
			slog.Warn("pitr", "database", d.Name, "err", err)
		}
	}
	return nil
}

func (w *PITR) ship(ctx context.Context, d db.ListPITRServicesRow, storage proto.Storage, rec string) error {
	now := w.Now()
	var last *db.WalBatch
	if rows, err := w.St.Q.ListWALBatches(ctx, d.ID); err != nil {
		return err
	} else if n := len(rows); n > 0 && rows[n-1].Files != "" {
		last = &rows[n-1]
	}
	args := proto.ShipWALArgs{Project: d.Project, Env: d.Env, Name: d.Name, Storage: storage, RecoveryPubkey: rec,
		KeyPrefix: fmt.Sprintf("yawl/%s/%s/%s/wal/", d.Project, d.Env, d.Name)}
	if last != nil {
		json.Unmarshal([]byte(last.Files), &args.Remove)
	}
	cmd, _ := proto.NewCommand(fmt.Sprintf("wal:%s:%d", d.ID, now.UnixNano()), proto.CmdShipWAL, args)
	tctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	res, err := w.GW.Send(tctx, d.ServerID, cmd)
	if err != nil {
		return err
	}
	var data proto.ShipWALData
	json.Unmarshal(res.Data, &data)
	var backlog int64
	if data.Batch != nil && len(data.Batch.Files) >= proto.WALBatchMax {
		backlog = 1
	}
	if res.OK {
		if err := w.St.Tx(ctx, func(q *db.Queries) error {
			if last != nil {
				if err := q.ClearWALBatchFiles(ctx, last.ID); err != nil {
					return err
				}
			}
			if b := data.Batch; b != nil {
				files, _ := json.Marshal(b.Files)
				return q.InsertWALBatch(ctx, db.InsertWALBatchParams{ID: store.NewID("wal_"), ServiceID: d.ID, ObjectKey: b.Key,
					FirstWal: b.First, LastWal: b.Last, Size: b.Size, Files: string(files), Sha256: b.SHA256, Backlog: backlog, CreatedAt: now.UnixMilli()})
			}
			return nil
		}); err != nil {
			return err
		}
		if backlog == 0 {
			w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: "pitr.shipped." + d.ID, Value: fmt.Sprint(now.UnixMilli())})
		}
	}
	if len(res.Data) > 0 {
		if err := w.lag(ctx, d, data.Pending, now); err != nil {
			return err
		}
	}
	if !res.OK {
		return fmt.Errorf("%s", res.Cause)
	}
	return nil
}

func (w *PITR) lag(ctx context.Context, d db.ListPITRServicesRow, pending int64, now time.Time) error {
	key := "pitr.lag." + d.ID
	if pending > walLagLimit {
		if last, err := w.St.Q.GetSetting(ctx, key); err == nil && last == now.UTC().Format("2006-01-02") {
			return nil
		}
		w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: key, Value: now.UTC().Format("2006-01-02")})
		return app.Announce(ctx, w.St.Q, now.UnixMilli(), "pitr.lag", d.ID, "system", app.WALLag(d.Project+"/"+d.Env+"/"+d.Name,
			fmt.Sprintf("%d MB waiting to upload", pending>>20),
			fmt.Sprintf("%s/%s: %s has %d MB of WAL waiting to upload; point in time recovery is falling behind. Check the backup storage.",
				d.Project, d.Env, d.Name, pending>>20)))
	}
	w.St.Q.DeleteSettingKey(ctx, key)
	return nil
}
