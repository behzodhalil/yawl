package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

type BackupView struct {
	ID         string           `json:"id"`
	State      string           `json:"state"`
	Size       int64            `json:"size,omitempty"`
	SHA256     string           `json:"sha256,omitempty"`
	Cause      string           `json:"cause,omitempty"`
	CreatedAt  int64            `json:"created_at"`
	FinishedAt int64            `json:"finished_at,omitempty"`
	Volume     string           `json:"volume,omitempty"`
	Copies     []BackupCopyView `json:"copies,omitempty"`
}

type BackupCopyView struct {
	Copy  string `json:"copy"`
	State string `json:"state"`
	Cause string `json:"cause,omitempty"`
}

func (h *handler) routesDB(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/backups", h.backup)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/backups", h.listBackups)
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/restore", h.restore)
	mux.HandleFunc("GET /api/services/{project}/{env}/{name}/pitr", h.pitr)
}

func (h *handler) restore(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.service(w, r)
	if !ok {
		return
	}
	var in struct {
		Backup      string `json:"backup"`
		Into        string `json:"into"`
		RecoveryBox string `json:"recovery_box"`
		At          string `json:"at"`
		From        string `json:"from"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	if _, err := base64.StdEncoding.DecodeString(in.RecoveryBox); err != nil || in.RecoveryBox == "" {
		http.Error(w, "recovery_box must be the recovery key sealed to the agent (base64)", http.StatusBadRequest)
		return
	}
	if in.At != "" && in.From != "" {
		http.Error(w, "a restore to a time reads the main storage only; leave out from", http.StatusBadRequest)
		return
	}
	var id string
	var err error
	if in.At != "" {
		at, perr := time.Parse(time.RFC3339, in.At)
		if perr != nil {
			http.Error(w, "at is a time like 2026-10-30T17:40:00Z", http.StatusBadRequest)
			return
		}
		id, err = h.a.RestoreAt(r.Context(), svc.ID, at, in.Into, in.RecoveryBox, actor(r))
	} else if spec.IsDatabase(spec.Kind(svc.Kind)) {
		id, err = h.a.RestoreFrom(r.Context(), svc.ID, in.Backup, in.Into, in.RecoveryBox, in.From, actor(r))
	} else {
		id, err = h.a.RestoreVolumeFrom(r.Context(), svc.ID, in.Backup, in.RecoveryBox, in.From, actor(r))
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"deploy": id})
}

func (h *handler) service(w http.ResponseWriter, r *http.Request) (db.GetServiceByNameRow, bool) {
	svc, err := h.a.St.Q.GetServiceByName(r.Context(), db.GetServiceByNameParams{
		Project: r.PathValue("project"), Env: r.PathValue("env"), Service: r.PathValue("name")})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "no such service", http.StatusNotFound)
		return svc, false
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return svc, false
	}
	return svc, true
}

func (h *handler) backup(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.service(w, r)
	if !ok {
		return
	}
	var in struct {
		Volume string `json:"volume"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	var id string
	var err error
	if in.Volume != "" {
		id, err = h.a.BackupVolume(r.Context(), svc.ID, in.Volume, actor(r))
	} else {
		id, err = h.a.Backup(r.Context(), svc.ID, actor(r))
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"id": id})
}

func (h *handler) pitr(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.service(w, r)
	if !ok {
		return
	}
	from, to, shipped, err := h.a.PITRWindow(r.Context(), svc.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, struct {
		PITR    bool  `json:"pitr"`
		From    int64 `json:"from"`
		To      int64 `json:"to"`
		Shipped int64 `json:"shipped"`
	}{svc.Pitr == 1, from, to, shipped})
}

func (h *handler) listBackups(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.service(w, r)
	if !ok {
		return
	}
	rows, err := h.a.St.Q.ListBackups(r.Context(), svc.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	copies := map[string][]BackupCopyView{}
	if cs, err := h.a.St.Q.CopiesOfService(r.Context(), svc.ID); err == nil {
		for _, c := range cs {
			copies[c.BackupID] = append(copies[c.BackupID], BackupCopyView{c.Copy, c.State, c.Cause})
		}
	}
	out := []BackupView{}
	for _, b := range rows {
		if b.Kind == "base" {
			continue
		}
		out = append(out, BackupView{ID: b.ID, State: b.State, Size: b.Size, SHA256: b.Sha256, Cause: b.Cause,
			CreatedAt: b.CreatedAt, FinishedAt: b.FinishedAt.Int64, Volume: b.Volume, Copies: copies[b.ID]})
	}
	writeJSON(w, out)
}
