package statuspage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type PageRec struct {
	ID       string   `json:"id"`
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	ServerID string   `json:"server_id"`
	Domain   string   `json:"domain"`
	Watcher  bool     `json:"watcher"`
	Rows     []RowRec `json:"rows"`
}

type RowRec struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Subject string `json:"subject"`
}

type IncidentState string

const (
	IncidentDraft     IncidentState = "draft"
	IncidentPublished IncidentState = "published"
	IncidentDismissed IncidentState = "dismissed"
)

type IncidentRec struct {
	ID         string        `json:"id"`
	PageID     string        `json:"page_id"`
	Kind       string        `json:"kind"`
	Title      string        `json:"title"`
	Rows       []string      `json:"rows"`
	State      IncidentState `json:"state"`
	Auto       bool          `json:"auto"`
	StartsAt   int64         `json:"starts_at"`
	EndsAt     int64         `json:"ends_at"`
	ResolvedAt int64         `json:"resolved_at"`
	CreatedAt  int64         `json:"created_at"`
	Updates    []Update      `json:"updates"`
}

func (i IncidentRec) Shown() Incident {
	return Incident{Kind: i.Kind, Title: i.Title, Rows: i.Rows, Updates: i.Updates, StartsAt: i.StartsAt, EndsAt: i.EndsAt, ResolvedAt: i.ResolvedAt, CreatedAt: i.CreatedAt}
}

var ErrNotFound = errors.New("not found")

func Pages(ctx context.Context, db DB) ([]PageRec, error) {
	rs, err := db.QueryContext(ctx, `SELECT id, slug, title, server_id, domain, watcher FROM status_pages ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []PageRec{}
	for rs.Next() {
		var p PageRec
		if err := rs.Scan(&p.ID, &p.Slug, &p.Title, &p.ServerID, &p.Domain, &p.Watcher); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	rs.Close()
	for i := range out {
		if out[i].Rows, err = rows(ctx, db, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func rows(ctx context.Context, db DB, page string) ([]RowRec, error) {
	rs, err := db.QueryContext(ctx, `SELECT id, name, subject FROM status_rows WHERE page_id = ? ORDER BY position`, page)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []RowRec{}
	for rs.Next() {
		var r RowRec
		if err := rs.Scan(&r.ID, &r.Name, &r.Subject); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func SavePage(ctx context.Context, db DB, p PageRec, now int64) error {
	if _, err := db.ExecContext(ctx, `INSERT INTO status_pages (id, slug, title, server_id, domain, watcher, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET slug = excluded.slug, title = excluded.title, server_id = excluded.server_id, domain = excluded.domain, watcher = excluded.watcher, updated_at = excluded.updated_at`,
		p.ID, p.Slug, p.Title, p.ServerID, p.Domain, p.Watcher, now, now); err != nil {
		return err
	}
	if p.Watcher {
		if _, err := db.ExecContext(ctx, `UPDATE status_pages SET watcher = 0 WHERE id <> ?`, p.ID); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM status_rows WHERE page_id = ?`, p.ID); err != nil {
		return err
	}
	for i, r := range p.Rows {
		if _, err := db.ExecContext(ctx, `INSERT INTO status_rows (id, page_id, position, name, subject, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.ID, p.ID, i, r.Name, r.Subject, now, now); err != nil {
			return err
		}
	}
	return nil
}

func DeletePage(ctx context.Context, db DB, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM status_pages WHERE id = ?`, id)
	return found(res, err)
}

func found(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func Incidents(ctx context.Context, db DB, page string, since int64) ([]IncidentRec, error) {
	rs, err := db.QueryContext(ctx, `SELECT id, page_id, kind, title, rows_json, state, auto, starts_at, ends_at, resolved_at, created_at FROM status_incidents
WHERE page_id = ? AND (resolved_at = 0 OR resolved_at >= ?) AND (kind <> 'maintenance' OR ends_at >= ?) ORDER BY created_at DESC, id`, page, since, since)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []IncidentRec{}
	for rs.Next() {
		var i IncidentRec
		var rj string
		if err := rs.Scan(&i.ID, &i.PageID, &i.Kind, &i.Title, &rj, &i.State, &i.Auto, &i.StartsAt, &i.EndsAt, &i.ResolvedAt, &i.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(rj), &i.Rows)
		if i.Rows == nil {
			i.Rows = []string{}
		}
		out = append(out, i)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	rs.Close()
	for k := range out {
		if out[k].Updates, err = updates(ctx, db, out[k].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func updates(ctx context.Context, db DB, incident string) ([]Update, error) {
	rs, err := db.QueryContext(ctx, `SELECT id, created_at, body FROM status_updates WHERE incident_id = ? ORDER BY created_at DESC, id DESC`, incident)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []Update{}
	for rs.Next() {
		var u Update
		if err := rs.Scan(&u.ID, &u.At, &u.Body); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rs.Err()
}

func GetIncident(ctx context.Context, db DB, id string) (IncidentRec, error) {
	var page string
	if err := db.QueryRowContext(ctx, `SELECT page_id FROM status_incidents WHERE id = ?`, id).Scan(&page); errors.Is(err, sql.ErrNoRows) {
		return IncidentRec{}, ErrNotFound
	} else if err != nil {
		return IncidentRec{}, err
	}
	all, err := Incidents(ctx, db, page, 0)
	if err != nil {
		return IncidentRec{}, err
	}
	for _, i := range all {
		if i.ID == id {
			return i, nil
		}
	}
	return IncidentRec{}, ErrNotFound
}

func InsertIncident(ctx context.Context, db DB, i IncidentRec, body, updateID string, now int64) error {
	rj, _ := json.Marshal(i.Rows)
	pub := int64(0)
	if i.State == IncidentPublished {
		pub = now
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO status_incidents (id, page_id, kind, title, rows_json, state, auto, starts_at, ends_at, published_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, i.ID, i.PageID, i.Kind, i.Title, string(rj), i.State, i.Auto, i.StartsAt, i.EndsAt, pub, i.CreatedAt, now); err != nil {
		return err
	}
	if body == "" {
		return nil
	}
	return AddUpdate(ctx, db, i.ID, updateID, body, now)
}

func AddUpdate(ctx context.Context, db DB, incident, id, body string, now int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO status_updates (id, incident_id, body, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, id, incident, body, now, now)
	return err
}

func SetState(ctx context.Context, db DB, id string, state IncidentState, now int64) error {
	res, err := db.ExecContext(ctx, `UPDATE status_incidents SET state = ?, published_at = CASE WHEN ? = 'published' AND published_at = 0 THEN ? ELSE published_at END, updated_at = ? WHERE id = ?`,
		state, state, now, now, id)
	return found(res, err)
}

func Resolve(ctx context.Context, db DB, id string, now int64) error {
	res, err := db.ExecContext(ctx, `UPDATE status_incidents SET resolved_at = ?, updated_at = ? WHERE id = ? AND resolved_at = 0`, now, now, id)
	return found(res, err)
}

func DeleteIncident(ctx context.Context, db DB, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM status_incidents WHERE id = ?`, id)
	return found(res, err)
}

func DropAutoDrafts(ctx context.Context, db DB, page string, up map[string]bool) error {
	all, err := Incidents(ctx, db, page, 0)
	if err != nil {
		return err
	}
	for _, i := range all {
		if !i.Auto || i.State == IncidentPublished || len(i.Rows) == 0 {
			continue
		}
		clear := true
		for _, r := range i.Rows {
			clear = clear && up[r]
		}
		if clear {
			if err := DeleteIncident(ctx, db, i.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

type Route struct {
	Host, ServerID, PageID, DNS, DNSDetail, Cert string
	TriedAt                                      int64
}

func Routes(ctx context.Context, db DB) ([]Route, error) {
	rs, err := db.QueryContext(ctx, `SELECT host, server_id, page_id, dns, dns_detail, cert, tried_at FROM status_routes ORDER BY host`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []Route
	for rs.Next() {
		var r Route
		if err := rs.Scan(&r.Host, &r.ServerID, &r.PageID, &r.DNS, &r.DNSDetail, &r.Cert, &r.TriedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func SaveRoute(ctx context.Context, db DB, r Route) error {
	_, err := db.ExecContext(ctx, `INSERT INTO status_routes (host, server_id, page_id, dns, dns_detail, cert, tried_at) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (host, server_id) DO UPDATE SET page_id = excluded.page_id, dns = excluded.dns, dns_detail = excluded.dns_detail, cert = excluded.cert, tried_at = excluded.tried_at`,
		r.Host, r.ServerID, r.PageID, r.DNS, r.DNSDetail, r.Cert, r.TriedAt)
	return err
}

func DeleteRoute(ctx context.Context, db DB, host, server string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM status_routes WHERE host = ? AND server_id = ?`, host, server)
	return err
}

func HealthEvents(ctx context.Context, db DB, from int64) ([]Event, error) {
	rs, err := db.QueryContext(ctx, `SELECT kind, subject_id, created_at FROM events
WHERE kind IN ('service.down', 'service.recovered', 'server.offline', 'server.online') AND created_at >= ? ORDER BY created_at, id`, from)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []Event
	for rs.Next() {
		var e Event
		if err := rs.Scan(&e.Kind, &e.Subject, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rs.Err()
}
