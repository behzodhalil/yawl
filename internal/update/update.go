package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const DefaultBase = "https://useyawl.com/releases"

const SumsVersionPrefix = "# yawl "

func SumsHeader(version string) string { return SumsVersionPrefix + version }

var syncFile = (*os.File).Sync

var ErrNoKey = errors.New("This build has no release key, so it cannot update itself. Install a release build.")

type Updater struct {
	Base, Latest string
	Key          ed25519.PublicKey
	HTTP         *http.Client
}

func Default(base string) (Updater, error) {
	k, err := base64.StdEncoding.DecodeString(ReleaseKey)
	if ReleaseKey == "" || err != nil || len(k) != ed25519.PublicKeySize {
		return Updater{}, ErrNoKey
	}
	if base == "" {
		base = DefaultBase
	}
	base = strings.TrimRight(base, "/")
	return Updater{Base: base, Latest: base + "/latest", Key: k}, nil
}

func (u Updater) client() *http.Client {
	if u.HTTP != nil {
		return u.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (u Updater) get(ctx context.Context, url string, w io.Writer) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	res, err := u.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return res.StatusCode, nil
	}
	_, err = io.Copy(w, io.LimitReader(res.Body, 512<<20))
	return res.StatusCode, err
}

func (u Updater) LatestVersion(ctx context.Context) (string, error) {
	var buf bytes.Buffer
	code, err := u.get(ctx, u.Latest, &buf)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("the release list answered %d", code)
	}
	var out struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil || out.Tag == "" {
		return "", errors.New("the release list has no tag")
	}
	return strings.TrimPrefix(out.Tag, "v"), nil
}

func (u Updater) Fetch(ctx context.Context, version, goos, goarch, dir string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	name := "yawl-" + goos + "-" + goarch
	root := u.Base + "/v" + version + "/"
	var sums, sig bytes.Buffer
	if code, err := u.get(ctx, root+"SHA256SUMS", &sums); err != nil {
		return "", err
	} else if code != http.StatusOK {
		return "", fmt.Errorf("Release v%s has no %s.", version, name)
	}
	if code, err := u.get(ctx, root+"SHA256SUMS.sig", &sig); err != nil {
		return "", err
	} else if code == http.StatusNotFound {
		return "", fmt.Errorf("Release v%s has no SHA256SUMS.sig.", version)
	} else if code != http.StatusOK || !ed25519.Verify(u.Key, sums.Bytes(), sig.Bytes()) {
		return "", errors.New("The release signature does not match this build's key.")
	}
	head, _, _ := strings.Cut(sums.String(), "\n")
	named, versioned := strings.CutPrefix(head, SumsVersionPrefix)
	if !versioned {
		return "", errors.New("This release's SHA256SUMS does not name its version; it was made before version binding.")
	}
	if named != version {
		return "", fmt.Errorf("This release's SHA256SUMS names v%s, not v%s.", named, version)
	}
	want := ""
	sc := bufio.NewScanner(&sums)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		return "", fmt.Errorf("Release v%s has no %s.", version, name)
	}
	f, err := os.CreateTemp(dir, ".yawl-new-*")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	code, err := u.get(ctx, root+name, io.MultiWriter(f, h))
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("Release v%s has no %s.", version, name)
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return "", fmt.Errorf("%s does not match SHA256SUMS.", name)
	}
	if err := f.Chmod(0o755); err != nil {
		return "", err
	}
	if err := syncFile(f); err != nil { // on disk before Swap renames it over the running binary
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ok = true
	return f.Name(), nil
}

// Swap renames path over exe. A rename keeps the running process's file
// whole: it holds the old inode, and the next start reads the new one.
func Swap(path, exe string) error { return os.Rename(path, exe) }
