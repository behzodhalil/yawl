package cloud

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS accounts (id INTEGER PRIMARY KEY, github_id INTEGER NOT NULL UNIQUE, login TEXT NOT NULL,
  email TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS cells (name TEXT PRIMARY KEY, account_id INTEGER NOT NULL UNIQUE REFERENCES accounts(id),
  claim_token TEXT NOT NULL DEFAULT '', version TEXT NOT NULL DEFAULT '', made INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS retired (name TEXT PRIMARY KEY, until INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS invites (code TEXT PRIMARY KEY, used_by INTEGER REFERENCES accounts(id));
CREATE TABLE IF NOT EXISTS billing (account_id INTEGER PRIMARY KEY REFERENCES accounts(id), trial_ends INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT '', customer_id TEXT NOT NULL DEFAULT '', subscription_id TEXT NOT NULL DEFAULT '',
  paid_until INTEGER NOT NULL DEFAULT 0, event_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS dodo_events (id TEXT PRIMARY KEY, at INTEGER NOT NULL);`

const serverColumn = `ALTER TABLE cells ADD COLUMN server TEXT NOT NULL DEFAULT ''`

const retiredServer = `ALTER TABLE retired ADD COLUMN server TEXT NOT NULL DEFAULT ''`

const retiredAccount = `ALTER TABLE retired ADD COLUMN account INTEGER NOT NULL DEFAULT 0`

var (
	errTaken   = errors.New("taken")
	errHasCell = errors.New("has a cell")
	errFull    = errors.New("full")
	errInvite  = errors.New("invite")
	errUsed    = errors.New("invite used")
	errRetired = errors.New("retired")
	errChurn   = errors.New("deleted one lately")
)

type frontDB struct{ db *sql.DB }

type cellRow struct {
	Name, ClaimToken, Version, Server string
	Account                           int64
	Made                              bool
}

func openStore(path string) (*frontDB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)&_pragma=journal_mode(wal)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	for _, c := range []string{serverColumn, retiredServer, retiredAccount} {
		if _, err := db.Exec(c); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, err
		}
	}
	return &frontDB{db}, nil
}

func (s *frontDB) account(githubID int64, login, email string) (int64, error) {
	_, err := s.db.Exec(`INSERT INTO accounts (github_id, login, email, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (github_id) DO UPDATE SET login = excluded.login, email = excluded.email`, githubID, login, email, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	var id int64
	return id, s.db.QueryRow(`SELECT id FROM accounts WHERE github_id = ?`, githubID).Scan(&id)
}

func (s *frontDB) cellOf(account int64) (cellRow, bool) {
	var c cellRow
	err := s.db.QueryRow(`SELECT name, claim_token, version, server, account_id, made FROM cells WHERE account_id = ?`, account).Scan(&c.Name, &c.ClaimToken, &c.Version, &c.Server, &c.Account, &c.Made)
	return c, err == nil
}

func (s *frontDB) cells() ([]cellRow, error) {
	rows, err := s.db.Query(`SELECT name, claim_token, version, server, account_id, made FROM cells WHERE made = 1 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cellRow
	for rows.Next() {
		var c cellRow
		rows.Scan(&c.Name, &c.ClaimToken, &c.Version, &c.Server, &c.Account, &c.Made)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *frontDB) perServer() (map[string]int, error) { return countPerServer(s.db) }

func countPerServer(q interface {
	Query(string, ...any) (*sql.Rows, error)
}) (map[string]int, error) {
	rows, err := q.Query(`SELECT server, COUNT(*) FROM (SELECT server FROM cells UNION ALL SELECT server FROM retired WHERE until > ?) GROUP BY server`, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	n := map[string]int{}
	for rows.Next() {
		var srv string
		var c int
		rows.Scan(&srv, &c)
		n[srv] = c
	}
	return n, rows.Err()
}

func place(servers []CellServer, n map[string]int) int {
	best := -1
	for i, s := range servers {
		if n[s.Name] >= s.Cap {
			continue
		}
		if best < 0 || n[s.Name]*servers[best].Cap < n[servers[best].Name]*s.Cap {
			best = i
		}
	}
	return best
}

func (s *frontDB) reserve(name string, account int64, token, version string, max int, invite string, inviteOnly bool, servers []CellServer) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var held int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM retired WHERE account = ? AND until > ?`, account, time.Now().UnixMilli()).Scan(&held); err != nil {
		return "", err
	} else if held > 0 {
		return "", errChurn
	}
	per, err := countPerServer(tx)
	if err != nil {
		return "", err
	}
	n := 0
	for _, c := range per {
		n += c
	}
	at := place(servers, per)
	if max > 0 && n >= max || at < 0 {
		return "", errFull
	}
	var until int64
	if tx.QueryRow(`SELECT until FROM retired WHERE name = ?`, name).Scan(&until) == nil && until > time.Now().UnixMilli() {
		return "", errRetired
	}
	if inviteOnly {
		var used sql.NullInt64
		if err := tx.QueryRow(`SELECT used_by FROM invites WHERE code = ?`, invite).Scan(&used); err != nil {
			return "", errInvite
		} else if used.Valid {
			return "", errUsed
		}
		if _, err := tx.Exec(`UPDATE invites SET used_by = ? WHERE code = ?`, account, invite); err != nil {
			return "", err
		}
	}
	srv := servers[at].Name
	_, err = tx.Exec(`INSERT INTO cells (name, account_id, claim_token, version, server, created_at) VALUES (?, ?, ?, ?, ?, ?)`, name, account, token, version, srv, time.Now().UnixMilli())
	if err == nil {
		_, err = tx.Exec(`INSERT OR IGNORE INTO billing (account_id, trial_ends) VALUES (?, ?)`, account, time.Now().Add(trialDays*24*time.Hour).UnixMilli())
	}
	switch {
	case err != nil && strings.Contains(err.Error(), "cells.name"):
		return "", errTaken
	case err != nil && strings.Contains(err.Error(), "cells.account_id"):
		return "", errHasCell
	case err != nil:
		return "", err
	}
	return srv, tx.Commit()
}

func (s *frontDB) home(name string) error {
	_, err := s.db.Exec(`UPDATE cells SET server = ? WHERE server = ''`, name)
	if err == nil {
		_, err = s.db.Exec(`UPDATE retired SET server = ? WHERE server = ''`, name)
	}
	return err
}

func (s *frontDB) drop(name string, unmade bool) error {
	if unmade {
		if _, err := s.db.Exec(`UPDATE invites SET used_by = NULL WHERE used_by = (SELECT account_id FROM cells WHERE name = ?)`, name); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`DELETE FROM cells WHERE name = ?`, name)
	return err
}

func (s *frontDB) made(name string) error {
	_, err := s.db.Exec(`UPDATE cells SET made = 1 WHERE name = ?`, name)
	return err
}

func (s *frontDB) retire(name, server string, account int64, d time.Duration) error {
	_, err := s.db.Exec(`INSERT INTO retired (name, server, account, until) VALUES (?, ?, ?, ?) ON CONFLICT (name) DO UPDATE SET server = excluded.server, account = excluded.account, until = excluded.until`,
		name, server, account, time.Now().Add(d).UnixMilli())
	return err
}

func (s *frontDB) setVersion(name, version string) error {
	_, err := s.db.Exec(`UPDATE cells SET version = ? WHERE name = ?`, version, name)
	return err
}

func (s *frontDB) addInvites(codes []string) error {
	for _, c := range codes {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO invites (code) VALUES (?)`, c); err != nil {
			return err
		}
	}
	return nil
}

func (s *frontDB) login(id int64) string {
	var l string
	s.db.QueryRow(`SELECT login FROM accounts WHERE id = ?`, id).Scan(&l)
	return l
}

func (s *frontDB) person(id int64) (login, email string) {
	s.db.QueryRow(`SELECT login, email FROM accounts WHERE id = ?`, id).Scan(&login, &email)
	return login, email
}

type billingRow struct {
	TrialEndsMs, PaidUntilMs int64
	Status, Customer, Subs   string
}

func (s *frontDB) billingOf(account int64) billingRow {
	var b billingRow
	s.db.QueryRow(`SELECT COALESCE(b.trial_ends, c.created_at + ?, 0), COALESCE(b.status, ''), COALESCE(b.customer_id, ''), COALESCE(b.subscription_id, ''),
		COALESCE(b.paid_until, 0) FROM accounts a LEFT JOIN billing b ON b.account_id = a.id LEFT JOIN cells c ON c.account_id = a.id WHERE a.id = ?`,
		(trialDays*24*time.Hour).Milliseconds(), account).Scan(&b.TrialEndsMs, &b.Status, &b.Customer, &b.Subs, &b.PaidUntilMs)
	return b
}

func (s *frontDB) accountOfCell(name string) (int64, bool) {
	var id int64
	return id, s.db.QueryRow(`SELECT account_id FROM cells WHERE name = ?`, name).Scan(&id) == nil
}

type subscription struct {
	Account              int64
	ID, Customer, Status string
	PaidUntilMs, AtMs    int64
}

func (s *frontDB) takeEvent(id string, e subscription) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if res, err := tx.Exec(`INSERT OR IGNORE INTO dodo_events (id, at) VALUES (?, ?)`, id, time.Now().UnixMilli()); err != nil {
		return false, err
	} else if n, _ := res.RowsAffected(); n == 0 {
		return true, nil
	}
	var account int64
	if (e.ID == "" || tx.QueryRow(`SELECT account_id FROM billing WHERE subscription_id = ?`, e.ID).Scan(&account) != nil) &&
		tx.QueryRow(`SELECT id FROM accounts WHERE id = ?`, e.Account).Scan(&account) != nil {
		return false, tx.Commit()
	}
	if _, err := tx.Exec(`INSERT INTO billing (account_id, trial_ends, status, customer_id, subscription_id, paid_until, event_at) VALUES (?, 0, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id) DO UPDATE SET status = excluded.status, customer_id = COALESCE(NULLIF(excluded.customer_id, ''), billing.customer_id),
		subscription_id = excluded.subscription_id, paid_until = excluded.paid_until, event_at = excluded.event_at
		WHERE excluded.event_at > billing.event_at AND (billing.subscription_id IN ('', excluded.subscription_id)
		  OR billing.status NOT IN ('active', 'renewed', 'unpaused', 'past_due', 'on_hold'))`, account, e.Status, e.Customer, e.ID, e.PaidUntilMs, e.AtMs); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
