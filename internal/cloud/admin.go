package cloud

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	versionRe    = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	releaseFiles = []string{"yawl-linux-amd64", "yawl-linux-arm64", "SHA256SUMS", "SHA256SUMS.sig", "install.sh"}
)

func (f *front) routesAdmin(mux *http.ServeMux) {
	mux.HandleFunc("PUT /admin/releases/{version}/{file}", f.admin(f.putRelease))
	mux.HandleFunc("GET /releases/latest", f.latest)
	mux.HandleFunc("GET /releases/{version}/{file}", f.getRelease)
	mux.HandleFunc("GET /install.sh", f.installScript)
	mux.HandleFunc("POST /admin/upgrade", f.admin(f.upgrade))
	mux.HandleFunc("GET /admin/servers", f.admin(f.servers))
}

func (f *front) servers(w http.ResponseWriter, r *http.Request) {
	n, err := f.st.perServer()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type row struct {
		Name  string `json:"name"`
		Cells int    `json:"cells"`
		Cap   int    `json:"cap"`
	}
	out := []row{}
	for _, s := range f.cfg.Servers {
		out = append(out, row{s.Name, n[s.Name], s.Cap})
	}
	json.NewEncoder(w).Encode(out)
}

func (f *front) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || f.cfg.AdminToken == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(f.cfg.AdminToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (f *front) releasePath(r *http.Request) (string, bool) {
	v, file := r.PathValue("version"), r.PathValue("file")
	if !versionRe.MatchString(v) || !slices.Contains(releaseFiles, file) {
		return "", false
	}
	return filepath.Join(f.cfg.Data, "releases", v, file), true
}

func (f *front) putRelease(w http.ResponseWriter, r *http.Request) {
	p, ok := f.releasePath(r)
	if !ok {
		http.Error(w, "a release is v<x.y.z>/ one of "+strings.Join(releaseFiles, ", "), http.StatusBadRequest)
		return
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	tmp := p + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, err = io.Copy(out, http.MaxBytesReader(w, r.Body, 256<<20))
	out.Close()
	if err != nil {
		os.Remove(tmp)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *front) getRelease(w http.ResponseWriter, r *http.Request) {
	p, ok := f.releasePath(r)
	if _, err := os.Stat(p); !ok || err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, p)
}

func (f *front) newest() string {
	dirs, _ := os.ReadDir(filepath.Join(f.cfg.Data, "releases"))
	best, bestN := "", []int(nil)
	for _, d := range dirs {
		if !versionRe.MatchString(d.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(f.cfg.Data, "releases", d.Name(), "install.sh")); err != nil {
			continue
		}
		var n []int
		for _, x := range strings.Split(d.Name()[1:], ".") {
			i, _ := strconv.Atoi(x)
			n = append(n, i)
		}
		if bestN == nil || slices.Compare(n, bestN) > 0 {
			best, bestN = d.Name(), n
		}
	}
	return best
}

func (f *front) latest(w http.ResponseWriter, r *http.Request) {
	v := f.newest()
	if v == "" {
		http.NotFound(w, r)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"tag_name": v}) // the shape GitHub's latest release has
}

func (f *front) installScript(w http.ResponseWriter, r *http.Request) {
	v := f.newest()
	if v == "" {
		http.Error(w, "No release yet.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript")
	http.ServeFile(w, r, filepath.Join(f.cfg.Data, "releases", v, "install.sh"))
}

var final = map[string]bool{"live": true, "failed": true, "rolled_back": true, "superseded": true}

func (f *front) upgrade(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Image         string
		Batch, Budget *int
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in)
	version, tagged := imageTag(in.Image)
	batch := 10
	if in.Batch != nil {
		batch = *in.Batch
	}
	if err != nil || !tagged || batch < 1 || in.Budget != nil && *in.Budget < 0 {
		http.Error(w, `send {"image": "<registry>/yawl:<version>", "batch": 10, "budget": 1}; batch and budget may be left out`, http.StatusBadRequest)
		return
	}
	cells, err := f.st.cells()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var todo []cellRow
	for _, c := range cells {
		if c.Version != version {
			todo = append(todo, c)
		}
	}
	budget := len(todo) / 100
	if in.Budget != nil {
		budget = *in.Budget
	}
	upgraded, failed := []string{}, map[string]string{}
	var mu sync.Mutex
	run := func(group []cellRow) {
		var wg sync.WaitGroup
		for _, c := range group {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cause := f.roll(r.Context(), c, in.Image, version)
				mu.Lock()
				defer mu.Unlock()
				if cause != "" {
					failed[c.Name] = cause
				} else {
					upgraded = append(upgraded, c.Name)
				}
			}()
		}
		wg.Wait()
	}
	i := 0
	for i < len(todo) {
		n := min(batch, len(todo)-i)
		if i == 0 {
			n = 1
		}
		run(todo[i : i+n])
		i += n
		if len(failed) > budget || i == 1 && len(failed) > 0 {
			break
		}
	}
	left := []string{}
	for _, c := range todo[i:] {
		left = append(left, c.Name)
	}
	slices.Sort(upgraded)
	if len(failed) > 0 {
		w.WriteHeader(http.StatusConflict)
	}
	json.NewEncoder(w).Encode(map[string]any{"upgraded": upgraded, "failed": failed, "left": left})
}

func (f *front) roll(ctx context.Context, c cellRow, image, version string) string {
	poll, wait := f.cfg.Poll, f.cfg.Wait
	if poll == 0 {
		poll = 5 * time.Second
	}
	if wait == 0 {
		wait = 10 * time.Minute
	}
	state, err := "", f.cfg.Cells.SetImage(ctx, c.Name, image)
	for until := time.Now().Add(wait); err == nil && !final[state] && time.Now().Before(until); {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(poll):
			state, err = f.cfg.Cells.State(ctx, c.Name)
		}
	}
	if err == nil && state == "live" {
		f.st.setVersion(c.Name, version)
		return ""
	}
	cause := "its deploy ended " + state
	if err != nil {
		cause = err.Error()
	} else if !final[state] {
		cause = "its deploy did not finish in time"
	}
	if c.Version != "" {
		f.cfg.Cells.SetImage(context.WithoutCancel(ctx), c.Name, strings.TrimSuffix(image, version)+c.Version)
	}
	return cause
}
