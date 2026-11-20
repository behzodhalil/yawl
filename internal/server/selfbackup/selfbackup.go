package selfbackup

import (
	"archive/tar"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/agekey"
)

var EtcFiles = []string{"ca.crt", "ca.key", "server.key"}

const KeySetting = "server_backup.sign_key"

func Name(key []byte, stamp string, enc []byte) string {
	return Prefix + stamp + "." + sign(key, stamp, enc) + ".tar.age"
}

func Signed(name string) bool {
	_, _, ok := split(name)
	return ok
}

func Valid(key []byte, name string, enc []byte) bool {
	stamp, sig, ok := split(name)
	return ok && hmac.Equal([]byte(sig), []byte(sign(key, stamp, enc)))
}

func split(name string) (stamp, sig string, ok bool) {
	return strings.Cut(strings.TrimSuffix(strings.TrimPrefix(name, Prefix), ".tar.age"), ".")
}

func sign(key []byte, stamp string, enc []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(stamp + "\x00"))
	m.Write(enc)
	return hex.EncodeToString(m.Sum(nil))
}

const dbName = "yawl.db"

func Make(ctx context.Context, db *sql.DB, dataDir, etcDir string, to [32]byte, w io.Writer) error {
	f, err := os.CreateTemp(dataDir, "yawl-vacuum-*.db")
	if err != nil {
		return err
	}
	tmp := f.Name()
	f.Close()
	os.Remove(tmp) // VACUUM INTO refuses an existing file
	defer os.Remove(tmp)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	r, err := agekey.Recipient(to)
	if err != nil {
		return err
	}
	enc, err := age.Encrypt(w, r)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(enc)
	add := func(name, path string) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: time.Now()}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	}
	if err := add(dbName, tmp); err != nil {
		return err
	}
	for _, n := range EtcFiles {
		if err := add(n, filepath.Join(etcDir, n)); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return enc.Close()
}

func Unpack(r io.Reader, id *age.X25519Identity, dataDir, etcDir string, prepare func(stagedDB string) error) error {
	plain, err := age.Decrypt(r, id)
	var noMatch *age.NoIdentityMatchError
	if errors.As(err, &noMatch) {
		return errors.New("This backup was encrypted to another recovery key.")
	} else if err != nil {
		return err
	}
	dest := map[string]string{dbName: dataDir}
	for _, n := range EtcFiles {
		dest[n] = etcDir
	}
	var staged [][2]string
	defer func() {
		for _, s := range staged {
			os.Remove(s[0])
		}
	}()
	tr := tar.NewReader(plain)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("read the backup: %w", err)
		}
		dir, ok := dest[h.Name]
		if !ok {
			return fmt.Errorf("the backup holds an unexpected file %q", h.Name)
		}
		f, err := os.CreateTemp(dir, ".restore-*.db")
		if err != nil {
			return err
		}
		staged = append(staged, [2]string{f.Name(), filepath.Join(dir, h.Name)})
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		if err := f.Chmod(0o600); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		delete(dest, h.Name)
	}
	if len(dest) > 0 {
		return errors.New("the backup is incomplete")
	}
	for _, st := range staged {
		if prepare != nil && filepath.Base(st[1]) == dbName {
			if err := prepare(st[0]); err != nil {
				return err
			}
		}
	}
	for _, s := range staged {
		if err := os.Rename(s[0], s[1]); err != nil {
			return err
		}
	}
	staged = nil
	return nil
}
