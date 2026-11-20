package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store"
)

func (h *handler) reports(ctx context.Context) (map[string]proto.ReportData, []string, error) {
	servers, err := h.a.St.Q.ListServers(ctx)
	if err != nil {
		return nil, nil, err
	}
	reports, missing := map[string]proto.ReportData{}, []string{}
	for _, s := range servers {
		cmd, _ := proto.NewCommand("report:"+store.NewID("rp_"), proto.CmdReport, struct{}{})
		tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		res, err := h.gw.Send(tctx, s.ID, cmd)
		cancel()
		var data proto.ReportData
		if err != nil || res.Cause != "" || json.Unmarshal(res.Data, &data) != nil {
			missing = append(missing, s.Name)
			continue
		}
		reports[s.ID] = data
	}
	return reports, missing, nil
}

func (h *handler) adoptView(w http.ResponseWriter, r *http.Request) {
	if !h.a.Adopting(r.Context()) {
		writeJSON(w, map[string]any{"adopting": false, "lines": []string{}})
		return
	}
	reports, missing, err := h.reports(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lines, err := h.a.AdoptView(r.Context(), reports, missing)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, map[string]any{"adopting": true, "lines": lines, "seen": seenOf(lines)})
}

func seenOf(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func (h *handler) adopt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Without []string
		Seen    string
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
	reports, missing, err := h.reports(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lines, err := h.a.AdoptView(r.Context(), reports, missing)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	missing = slices.DeleteFunc(missing, func(m string) bool { return slices.Contains(in.Without, m) })
	if len(missing) > 0 {
		http.Error(w, "Some servers have not reported yet: "+strings.Join(missing, ", ")+".", http.StatusConflict)
		return
	}
	if lines == nil {
		lines = []string{}
	}
	if in.Seen != seenOf(lines) {
		http.Error(w, "What the servers run changed since you looked; review it again.", http.StatusConflict)
		return
	}
	if err := h.a.Adopt(r.Context(), reports, actor(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
