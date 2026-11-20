package vault

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/nacl/secretbox"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

const prefix = "v1:"

type Vault struct{ key [32]byte }

func LoadOrCreate(etcDir string) (*Vault, error) {
	p := filepath.Join(etcDir, "server.key")
	b, err := readFull(p)
	if errors.Is(err, os.ErrNotExist) {
		b = make([]byte, 32)
		rand.Read(b)
		if err := os.MkdirAll(etcDir, 0o700); err != nil {
			return nil, err
		}
		if f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); errors.Is(err, os.ErrExist) {
			if b, err = readFull(p); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		} else {
			_, err = f.Write(b)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return nil, err
			}
		}
	} else if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("%s is %d bytes, want 32", p, len(b))
	}
	v := &Vault{}
	copy(v.key[:], b)
	return v, nil
}

func (v *Vault) Seal(plain []byte) string {
	var nonce [24]byte
	rand.Read(nonce[:])
	return prefix + base64.StdEncoding.EncodeToString(secretbox.Seal(nonce[:], plain, &nonce, &v.key))
}

func (v *Vault) Open(s string) ([]byte, error) {
	if !strings.HasPrefix(s, prefix) {
		return nil, errors.New("vault: not sealed")
	}
	b, err := base64.StdEncoding.DecodeString(s[len(prefix):])
	if err != nil || len(b) < 24 {
		return nil, errors.New("vault: bad ciphertext")
	}
	var nonce [24]byte
	copy(nonce[:], b)
	out, ok := secretbox.Open(nil, b[24:], &nonce, &v.key)
	if !ok {
		return nil, errors.New("vault: cannot open (another server.key?)")
	}
	return out, nil
}

func SetSecret(ctx context.Context, q *db.Queries, v *Vault, key string, val []byte) error {
	return q.SetSetting(ctx, db.SetSettingParams{Key: key, Value: v.Seal(val)})
}

func GetSecret(ctx context.Context, q *db.Queries, v *Vault, key string) ([]byte, error) {
	s, err := q.GetSetting(ctx, key)
	if err != nil {
		return nil, err
	}
	return v.Open(s)
}

func readFull(p string) ([]byte, error) {
	for i := 0; ; i++ {
		b, err := os.ReadFile(p)
		if err != nil || len(b) == 32 || i == 50 {
			return b, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
