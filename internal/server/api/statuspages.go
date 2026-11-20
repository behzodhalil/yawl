package api

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/behzodhalil/yawl/internal/server/statuspage"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/spec"
)

func (h *handler) routesStatusPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status-pages", h.statusPages)
	mux.HandleFunc("POST /api/status-pages", h.saveStatusPage)
	mux.HandleFunc("PUT /api/status-pages/{id}", h.saveStatusPage)
	mux.HandleFunc("DELETE /api/status-pages/{id}", h.deleteStatusPage)
	mux.HandleFunc("GET /api/status-pages/{id}/preview", h.previewStatusPage)
	mux.HandleFunc("GET /api/status-pages/{id}/incidents", h.statusIncidents)
	mux.HandleFunc("POST /api/status-pages/{id}/incidents", h.addStatusIncident)
	mux.HandleFunc("POST /api/status-incidents/{id}/updates", h.addStatusUpdate)
	mux.HandleFunc("POST /api/status-incidents/{id}/publish", h.publishStatusIncident)
	mux.HandleFunc("DELETE /api/status-incidents/{id}", h.deleteStatusIncident)
}

type statusHost struct {
	Host      string `json:"host"`
	DNS       string `json:"dns"`
	DNSDetail string `json:"dns_detail"`
	Cert      string `json:"cert"`
}

type statusPageView struct {
	statuspage.PageRec
	URL   string       `json:"url"`
	Hosts []statusHost `json:"hosts"`
}

type statusChoice struct {
	Subject string `json:"subject"`
	Label   string `json:"label"`
}

type statusServer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (h *handler) statusChoices(ctx context.Context) ([]statusChoice, error) {
	out := []statusChoice{{Subject: "panel", Label: "The panel"}}
	rs, err := h.a.St.DB.QueryContext(ctx, `SELECT p.id, p.name FROM projects p WHERE p.deleted_at IS NULL ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var id, name string
		if err := rs.Scan(&id, &name); err != nil {
			rs.Close()
			return nil, err
		}
		out = append(out, statusChoice{Subject: "project:" + id, Label: "Project " + name + ", every service in it"})
	}
	rs.Close()
	rs, err = h.a.St.DB.QueryContext(ctx, `SELECT s.id, p.name, e.name, s.name FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE s.deleted_at IS NULL AND s.kind <> 'cron' AND e.preview_of = '' ORDER BY p.name, e.name, s.name`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		var id, p, e, s string
		if err := rs.Scan(&id, &p, &e, &s); err != nil {
			return nil, err
		}
		out = append(out, statusChoice{Subject: "service:" + id, Label: p + "/" + e + "/" + s})
	}
	return out, rs.Err()
}

func (h *handler) statusPages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pages, err := statuspage.Pages(ctx, h.a.St.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	routes, err := statuspage.Routes(ctx, h.a.St.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	choices, err := h.statusChoices(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	servers, err := h.a.St.Q.ListServers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := struct {
		Pages   []statusPageView `json:"pages"`
		Choices []statusChoice   `json:"choices"`
		Servers []statusServer   `json:"servers"`
	}{Pages: []statusPageView{}, Choices: choices, Servers: []statusServer{}}
	ips := map[string]string{}
	for _, s := range servers {
		out.Servers = append(out.Servers, statusServer{ID: s.ID, Name: s.Name})
		ips[s.ID] = s.PublicIp
	}
	for _, p := range pages {
		v := statusPageView{PageRec: p, Hosts: []statusHost{}}
		if ip, err := netip.ParseAddr(ips[p.ServerID]); err == nil {
			v.URL = "https://" + statuspage.FreeHost(p.Slug, ip)
		}
		for _, rt := range routes {
			if rt.PageID == p.ID && rt.ServerID == p.ServerID {
				v.Hosts = append(v.Hosts, statusHost{Host: rt.Host, DNS: rt.DNS, DNSDetail: rt.DNSDetail, Cert: rt.Cert})
			}
		}
		out.Pages = append(out.Pages, v)
	}
	writeJSON(w, out)
}

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

func short(s, what string, most int) (string, string) {
	s = strings.TrimSpace(s)
	switch n := utf8.RuneCountInString(s); {
	case n == 0:
		return "", what + " is needed."
	case n > most:
		return "", what + " is too long."
	}
	return s, ""
}

func (h *handler) saveStatusPage(w http.ResponseWriter, r *http.Request) {
	var in statuspage.PageRec
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	in.ID = r.PathValue("id")
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Domain), "."))
	var why string
	if in.Title, why = short(in.Title, "A title", statuspage.MaxTitle); why != "" {
		http.Error(w, why, http.StatusBadRequest)
		return
	}
	if !slugRe.MatchString(in.Slug) {
		http.Error(w, "The short name is lowercase letters, digits and dashes, up to 32.", http.StatusBadRequest)
		return
	}
	if _, err := h.a.St.Q.GetServer(ctx, in.ServerID); err != nil {
		http.Error(w, "Choose a server to serve the page.", http.StatusBadRequest)
		return
	}
	pages, err := statuspage.Pages(ctx, h.a.St.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if in.ID != "" && !slices.ContainsFunc(pages, func(p statuspage.PageRec) bool { return p.ID == in.ID }) {
		http.Error(w, "no such status page", http.StatusNotFound)
		return
	}
	for _, p := range pages {
		if p.ID == in.ID {
			continue
		}
		if p.Slug == in.Slug {
			http.Error(w, "Another status page has the short name "+in.Slug+".", http.StatusBadRequest)
			return
		}
		if in.Domain != "" && p.Domain == in.Domain {
			http.Error(w, "Another status page uses "+in.Domain+".", http.StatusBadRequest)
			return
		}
	}
	if in.Domain != "" {
		taken, err := h.a.St.Q.DomainsOnHost(ctx, in.Domain)
		switch {
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		case !spec.ValidHost(in.Domain) || strings.Contains(in.Domain, "/"):
			http.Error(w, in.Domain+" is not a domain name.", http.StatusBadRequest)
			return
		case len(taken) > 0 || in.Domain == h.setting(ctx, "panel.domain"):
			http.Error(w, in.Domain+" already points to a service or the panel.", http.StatusBadRequest)
			return
		}
	}
	if len(in.Rows) > statuspage.MaxRows {
		http.Error(w, "A status page shows at most 50 rows.", http.StatusBadRequest)
		return
	}
	choices, err := h.statusChoices(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for i := range in.Rows {
		row := &in.Rows[i]
		if row.Name, why = short(row.Name, "Each row's public name", statuspage.MaxName); why != "" {
			http.Error(w, why, http.StatusBadRequest)
			return
		}
		if !slices.ContainsFunc(choices, func(c statusChoice) bool { return c.Subject == row.Subject }) {
			http.Error(w, "A row covers a service, a project or the panel.", http.StatusBadRequest)
			return
		}
		if row.ID == "" || !strings.HasPrefix(row.ID, "sr_") {
			row.ID = store.NewID("sr_")
		}
	}
	if in.ID == "" {
		in.ID = store.NewID("sp_")
	}
	tx, err := h.a.St.DB.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := statuspage.SavePage(ctx, tx, in, h.a.Now().UnixMilli()); err != nil {
		tx.Rollback()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, in)
}

func (h *handler) deleteStatusPage(w http.ResponseWriter, r *http.Request) {
	if err := statuspage.DeletePage(r.Context(), h.a.St.DB, r.PathValue("id")); errors.Is(err, statuspage.ErrNotFound) {
		http.Error(w, "no such status page", http.StatusNotFound)
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handler) statusPage(w http.ResponseWriter, r *http.Request) (statuspage.PageRec, bool) {
	pages, err := statuspage.Pages(r.Context(), h.a.St.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return statuspage.PageRec{}, false
	}
	for _, p := range pages {
		if p.ID == r.PathValue("id") {
			return p, true
		}
	}
	http.Error(w, "no such status page", http.StatusNotFound)
	return statuspage.PageRec{}, false
}

func (h *handler) previewStatusPage(w http.ResponseWriter, r *http.Request) {
	p, ok := h.statusPage(w, r)
	if !ok {
		return
	}
	ctx, now := r.Context(), h.a.Now()
	world, err := statuspage.Load(ctx, h.a.St.DB, nil, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	incs, err := statuspage.Incidents(ctx, h.a.St.DB, p.ID, now.UnixMilli()-14*86_400_000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Write(statuspage.Render(world.Page(p, incs, now)))
}

func (h *handler) statusIncidents(w http.ResponseWriter, r *http.Request) {
	p, ok := h.statusPage(w, r)
	if !ok {
		return
	}
	list, err := statuspage.Incidents(r.Context(), h.a.St.DB, p.ID, h.a.Now().UnixMilli()-90*86_400_000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, list)
}

func (h *handler) addStatusIncident(w http.ResponseWriter, r *http.Request) {
	var in struct {
		statuspage.IncidentRec
		Body    string `json:"body"`
		Publish bool   `json:"publish"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	p, ok := h.statusPage(w, r)
	if !ok {
		return
	}
	now := h.a.Now().UnixMilli()
	i := statuspage.IncidentRec{ID: store.NewID("si_"), PageID: p.ID, Kind: in.Kind, State: "draft", CreatedAt: now}
	if in.Publish {
		i.State = "published"
	}
	var why string
	if i.Title, why = short(in.Title, "A title", statuspage.MaxTitle); why != "" {
		http.Error(w, why, http.StatusBadRequest)
		return
	}
	body := strings.TrimSpace(in.Body)
	switch {
	case i.Kind != statuspage.KindIncident && i.Kind != statuspage.KindMaint:
		http.Error(w, "An incident is an incident or maintenance.", http.StatusBadRequest)
		return
	case utf8.RuneCountInString(body) > statuspage.MaxUpdate:
		http.Error(w, "An update is at most 2000 characters.", http.StatusBadRequest)
		return
	case i.Kind == statuspage.KindMaint && (in.StartsAt <= 0 || in.EndsAt <= in.StartsAt):
		http.Error(w, "Maintenance needs a start and an end after it.", http.StatusBadRequest)
		return
	}
	if i.Kind == statuspage.KindMaint {
		i.StartsAt, i.EndsAt = in.StartsAt, in.EndsAt
	}
	for _, id := range in.Rows {
		if !slices.ContainsFunc(p.Rows, func(x statuspage.RowRec) bool { return x.ID == id }) {
			http.Error(w, "An incident names rows of its own page.", http.StatusBadRequest)
			return
		}
		i.Rows = append(i.Rows, id)
	}
	if i.Rows == nil {
		i.Rows = []string{}
	}
	if err := statuspage.InsertIncident(r.Context(), h.a.St.DB, i, body, store.NewID("su_"), now); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, i)
}

func (h *handler) statusIncident(w http.ResponseWriter, r *http.Request) (statuspage.IncidentRec, bool) {
	i, err := statuspage.GetIncident(r.Context(), h.a.St.DB, r.PathValue("id"))
	if errors.Is(err, statuspage.ErrNotFound) {
		http.Error(w, "no such incident", http.StatusNotFound)
		return i, false
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return i, false
	}
	return i, true
}

func (h *handler) addStatusUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body    string `json:"body"`
		Resolve bool   `json:"resolve"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	i, ok := h.statusIncident(w, r)
	if !ok {
		return
	}
	body, why := short(in.Body, "An update", statuspage.MaxUpdate)
	switch {
	case why != "":
		http.Error(w, why, http.StatusBadRequest)
		return
	case in.Resolve && (i.Kind != statuspage.KindIncident || i.ResolvedAt != 0):
		http.Error(w, "Only an open incident can be resolved.", http.StatusBadRequest)
		return
	}
	ctx, now := r.Context(), h.a.Now().UnixMilli()
	tx, err := h.a.St.DB.BeginTx(ctx, nil)
	if err == nil {
		err = statuspage.AddUpdate(ctx, tx, i.ID, store.NewID("su_"), body, now)
	}
	if err == nil && in.Resolve {
		err = statuspage.Resolve(ctx, tx, i.ID, now)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		tx.Rollback()
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) publishStatusIncident(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.statusIncident(w, r); !ok {
		return
	}
	if err := statuspage.SetState(r.Context(), h.a.St.DB, r.PathValue("id"), "published", h.a.Now().UnixMilli()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) deleteStatusIncident(w http.ResponseWriter, r *http.Request) {
	i, ok := h.statusIncident(w, r)
	if !ok {
		return
	}
	var err error
	if i.Auto && i.State == "draft" {
		err = statuspage.SetState(r.Context(), h.a.St.DB, i.ID, "dismissed", h.a.Now().UnixMilli())
	} else {
		err = statuspage.DeleteIncident(r.Context(), h.a.St.DB, i.ID)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
