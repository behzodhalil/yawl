package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/behzodhalil/yawl/internal/update"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	switch {
	case len(args) == 2 && args[0] == "keygen":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := f.Write([]byte(base64.StdEncoding.EncodeToString(priv) + "\n")); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Fprintln(out, base64.StdEncoding.EncodeToString(pub))
		return nil
	case len(args) == 2 && args[0] == "pub":
		priv, err := readKey(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(out, base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
		return nil
	case len(args) == 2 && args[0] == "pem":
		priv, err := readKey(args[1])
		if err != nil {
			return err
		}
		der, err := x509.MarshalPKIXPublicKey(priv.Public())
		if err != nil {
			return err
		}
		return pem.Encode(out, &pem.Block{Type: "PUBLIC KEY", Bytes: der})
	case len(args) == 3 && args[0] == "stamp":
		script, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		var key strings.Builder
		if err := run([]string{"pem", args[2]}, &key); err != nil {
			return err
		}
		const line = `RELEASE_KEY_PEM=""`
		if strings.Count(string(script), line) != 1 {
			return errors.New(args[1] + ` has no RELEASE_KEY_PEM="" line`)
		}
		_, err = io.WriteString(out, strings.Replace(string(script), line, "RELEASE_KEY_PEM='"+key.String()+"'", 1))
		return err
	case len(args) == 5 && args[0] == "sums" && args[1] == "--version":
		dir := args[3]
		priv, err := readKey(args[4])
		if err != nil {
			return err
		}
		names, err := filepath.Glob(filepath.Join(dir, "yawl-*"))
		if err != nil || len(names) == 0 {
			return errors.New("no yawl-* binaries in " + dir)
		}
		sort.Strings(names)
		var sums strings.Builder
		fmt.Fprintln(&sums, update.SumsHeader(strings.TrimPrefix(args[2], "v")))
		for _, n := range names {
			b, err := os.ReadFile(n)
			if err != nil {
				return err
			}
			s := sha256.Sum256(b)
			fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(s[:]), filepath.Base(n))
		}
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "SHA256SUMS.sig"), ed25519.Sign(priv, []byte(sums.String())), 0o644)
	}
	return errors.New("usage: sign keygen <key path> | sign pub <key path> | sign pem <key path> | sign stamp <install.sh> <key path> | sign sums --version <version> <release dir> <key path>")
}

func readKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("that is not a release key file")
	}
	return priv, nil
}
