package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/crypto/curve25519"

	"github.com/behzodhalil/yawl/internal/seal"
)

const file = "secrets.key"

type Keyring struct {
	Public  seal.PublicKey
	private seal.PrivateKey
	masker  Masker
}

func LoadOrCreate(dir string) (*Keyring, error) {
	k, err := Load(dir)
	if !errors.Is(err, os.ErrNotExist) {
		return k, err
	}
	_, priv, err := seal.GenerateKey()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, file), priv[:], 0o600); err != nil {
		return nil, err
	}
	return Load(dir)
}

func Load(dir string) (*Keyring, error) {
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, errors.New("secrets.key is damaged")
	}
	k := &Keyring{}
	copy(k.private[:], b)
	pub, err := curve25519.X25519(b, curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	copy(k.Public[:], pub)
	return k, nil
}

func (k *Keyring) Open(sealed string) ([]byte, error) {
	v, err := seal.Open(k.Public, k.private, sealed)
	if err == nil {
		k.masker.Add(string(v))
	}
	return v, err
}

func (k *Keyring) OpenOnceUnmasked(sealed string) ([]byte, error) {
	return seal.Open(k.Public, k.private, sealed)
}

func (k *Keyring) Reseal(sealed string, to seal.PublicKey) (string, error) {
	v, err := seal.Open(k.Public, k.private, sealed)
	if err != nil {
		return "", err
	}
	return seal.Seal(to, v)
}

func (k *Keyring) Masker() *Masker { return &k.masker }

type Masker struct {
	mu     sync.Mutex
	values []string
}

const minMasked = 6

func (m *Masker) Add(values ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range values {
		for _, part := range append(strings.Split(v, "\n"), v) {
			part = strings.TrimSpace(part)
			if len(part) < minMasked {
				continue
			}
			for _, form := range append(jsonForms(part), part, url.QueryEscape(part), url.PathEscape(part)) {
				if !slices.Contains(m.values, form) {
					m.values = append(m.values, form)
				}
			}
		}
	}
	slices.SortFunc(m.values, func(a, b string) int { return len(b) - len(a) })
}

// jsonForms is v as a JSON string holds it, without the quotes: with and
// without Go's escaping of <, > and &.
func jsonForms(v string) []string {
	var out []string
	for _, html := range []bool{true, false} {
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(html)
		e.Encode(v)
		out = append(out, strings.TrimSuffix(strings.TrimSuffix(b.String(), "\n"), `"`)[1:])
	}
	return out
}

func (m *Masker) Mask(s string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var hit []bool
	for _, v := range m.values {
		for i := 0; ; i++ {
			j := strings.Index(s[i:], v)
			if j < 0 {
				break
			}
			if hit == nil {
				hit = make([]bool, len(s))
			}
			i += j
			for k := i; k < i+len(v); k++ {
				hit[k] = true
			}
		}
	}
	if hit == nil {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if !hit[i] {
			b.WriteByte(s[i])
			i++
			continue
		}
		b.WriteString("••••")
		for i < len(s) && hit[i] {
			i++
		}
	}
	return b.String()
}
