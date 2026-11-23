package db

import (
	"context"

	"github.com/behzodhalil/yawl/internal/spec"
)

// ListDatabaseServices lists the databases Yawl backs up every night, by
// spec.Service.BackupOn: one rule for the schedule, the missed alert and the
// watcher, so MariaDB and ClickHouse are not left out by a kind list in SQL. Written by hand: sqlc leaves this file alone.
func (q *Queries) ListDatabaseServices(ctx context.Context) ([]ListBackupCandidatesRow, error) {
	rows, err := q.ListBackupCandidates(ctx)
	if err != nil {
		return nil, err
	}
	var out []ListBackupCandidatesRow
	for _, r := range rows {
		on := r.Backup == 1
		if k := spec.Kind(r.Kind); spec.IsDatabase(k) && (spec.Service{Kind: k, Backup: &on}).BackupOn() {
			out = append(out, r)
		}
	}
	return out, nil
}
