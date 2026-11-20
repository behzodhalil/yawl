package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
)

const maxImport = 10 << 30

const importHeadroom = 1 << 30

var cellUploads int64 = maxImport

var uploading struct {
	sync.Mutex
	n int64
}

var freeBytes = func(dir string) uint64 {
	var s syscall.Statfs_t
	if syscall.Statfs(dir, &s) != nil {
		return math.MaxUint64
	}
	return s.Bavail * uint64(s.Bsize)
}

const errImportSize = "A dump upload is at most 10 GB; put a bigger one in /var/lib/yawl/agent/imports on the server, or give a link."

func keptBytes(dir string) int64 {
	var n int64
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		if info, err := f.Info(); err == nil {
			n += info.Size()
		}
	}
	return n
}

func (h *handler) importUpload(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.service(w, r)
	if !ok {
		return
	}
	name := filepath.Base(r.URL.Query().Get("name"))
	if name == "." || name == "/" {
		name = "dump"
	}
	if h.d.ImportDir == "" {
		http.Error(w, "uploads are not wired on this panel", http.StatusNotImplemented)
		return
	}
	if err := os.MkdirAll(h.d.ImportDir, 0o700); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	n := r.ContentLength
	if n > maxImport {
		http.Error(w, errImportSize, http.StatusRequestEntityTooLarge)
		return
	}
	uploading.Lock()
	free := freeBytes(h.d.ImportDir)
	room := int64(maxImport)
	if free < uint64(maxImport)+importHeadroom+uint64(uploading.n) {
		room = int64(free) - importHeadroom - uploading.n
	}
	full := fmt.Sprintf("The panel's disk has %d MB free; this dump needs %d MB and 1 GB to spare. Free some space, or give a link.", free>>20, max(n, 0)>>20)
	if h.d.Managed {
		if left := cellUploads - keptBytes(h.d.ImportDir) - uploading.n; left < room {
			room = left
			full = fmt.Sprintf("A Yawl Cloud panel keeps at most %d MB of uploads at once, waiting or arriving; let those import first, or give a link.", cellUploads>>20)
		}
	}
	take := n
	if n <= 0 {
		take = room
	}
	if room <= 0 || take > room {
		uploading.Unlock()
		http.Error(w, full, http.StatusInsufficientStorage)
		return
	}
	uploading.n += take
	uploading.Unlock()
	defer func() {
		uploading.Lock()
		uploading.n -= take
		uploading.Unlock()
	}()
	file := filepath.Join(h.d.ImportDir, store.NewID("upl_"))
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, take))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) && take < maxImport {
		os.Remove(file)
		http.Error(w, full, http.StatusInsufficientStorage)
		return
	} else if errors.As(err, &tooBig) {
		os.Remove(file)
		http.Error(w, errImportSize, http.StatusRequestEntityTooLarge)
		return
	} else if err != nil {
		os.Remove(file)
		http.Error(w, "the upload did not finish: "+err.Error(), http.StatusBadRequest)
		return
	}
	id, err := h.a.Import(r.Context(), svc.ID, "upload", name, file, actor(r))
	if err != nil {
		os.Remove(file)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"deploy": id})
}

func (h *handler) importFrom(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.service(w, r)
	if !ok {
		return
	}
	var in struct{ URL, Path string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	src, ref := "path", in.Path
	if in.URL != "" {
		src, ref = "url", in.URL
	}
	id, err := h.a.Import(r.Context(), svc.ID, src, ref, "", actor(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"deploy": id})
}

func ImportFile(a *app.App) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/imports/{id}", func(w http.ResponseWriter, r *http.Request) {
		imp, err := a.St.Q.GetImport(r.Context(), r.PathValue("id"))
		t := r.URL.Query().Get("t")
		if err != nil || imp.File == "" || t == "" || subtle.ConstantTimeCompare([]byte(t), []byte(imp.Token)) != 1 {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(imp.File)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, _ := f.Stat()
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "", st.ModTime(), f)
	})
	return mux
}
