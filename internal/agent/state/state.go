package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	_ "modernc.org/sqlite"

	"github.com/behzodhalil/yawl/internal/proto"
)

type State struct{ db *sql.DB }

func Open(path string) (*State, error) {
	d, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS results (key TEXT PRIMARY KEY, body TEXT NOT NULL, created_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS cron_runs (id TEXT PRIMARY KEY, job TEXT NOT NULL, scheduled_at BIGINT NOT NULL,
			started_at BIGINT NOT NULL, finished_at BIGINT NOT NULL DEFAULT 0, exit INTEGER NOT NULL DEFAULT 0,
			cause TEXT NOT NULL DEFAULT '', output TEXT NOT NULL DEFAULT '', skipped INTEGER NOT NULL DEFAULT 0,
			timed_out INTEGER NOT NULL DEFAULT 0)`,
	} {
		if _, err := d.Exec(q); err != nil {
			d.Close()
			return nil, err
		}
	}
	return &State{db: d}, nil
}

func (s *State) Close() error { return s.db.Close() }

func (s *State) Result(key string) (proto.Result, bool, error) {
	var body string
	err := s.db.QueryRow(`SELECT body FROM results WHERE key = ?`, key).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.Result{}, false, nil
	}
	if err != nil {
		return proto.Result{}, false, err
	}
	var r proto.Result
	return r, true, json.Unmarshal([]byte(body), &r)
}

func (s *State) SaveResult(r proto.Result) error {
	b, _ := json.Marshal(r)
	_, err := s.db.Exec(`INSERT INTO results (key, body, created_at) VALUES (?, ?, ?) ON CONFLICT (key) DO NOTHING`,
		r.Key, string(b), time.Now().UnixMilli())
	return err
}

func (s *State) AddCronRun(job string, r proto.CronRun) error {
	if _, err := s.db.Exec(`INSERT INTO cron_runs (id, job, scheduled_at, started_at, finished_at, exit, cause, output, skipped, timed_out)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, job, r.ScheduledAt, r.StartedAt, r.FinishedAt, r.Exit, r.Cause, r.Output, r.Skipped, r.TimedOut); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM cron_runs WHERE job = ? AND finished_at <> 0 AND id NOT IN (SELECT id FROM cron_runs WHERE job = ? ORDER BY rowid DESC LIMIT 20)`, job, job)
	return err
}

func (s *State) FinishCronRun(r proto.CronRun) error {
	_, err := s.db.Exec(`UPDATE cron_runs SET finished_at = ?, exit = ?, cause = ?, output = ?, timed_out = ? WHERE id = ?`,
		r.FinishedAt, r.Exit, r.Cause, r.Output, r.TimedOut, r.ID)
	return err
}

func scanRuns(rows *sql.Rows, err error) ([]proto.CronRun, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []proto.CronRun
	for rows.Next() {
		var r proto.CronRun
		if err := rows.Scan(&r.ID, &r.ScheduledAt, &r.StartedAt, &r.FinishedAt, &r.Exit, &r.Cause, &r.Output, &r.Skipped, &r.TimedOut); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *State) CronRuns(job string, n int) ([]proto.CronRun, error) {
	return scanRuns(s.db.Query(`SELECT id, scheduled_at, started_at, finished_at, exit, cause, output, skipped, timed_out
		FROM cron_runs WHERE job = ? ORDER BY rowid DESC LIMIT ?`, job, n))
}

func (s *State) StaleCronRuns() (map[string][]proto.CronRun, error) {
	out := map[string][]proto.CronRun{}
	jobs, err := s.db.Query(`SELECT DISTINCT job FROM cron_runs WHERE finished_at = 0 AND skipped = 0`)
	if err != nil {
		return nil, err
	}
	var names []string
	for jobs.Next() {
		var j string
		jobs.Scan(&j)
		names = append(names, j)
	}
	jobs.Close()
	for _, j := range names {
		runs, err := scanRuns(s.db.Query(`SELECT id, scheduled_at, started_at, finished_at, exit, cause, output, skipped, timed_out
			FROM cron_runs WHERE job = ? AND finished_at = 0 AND skipped = 0`, j))
		if err != nil {
			return nil, err
		}
		out[j] = runs
	}
	return out, nil
}
