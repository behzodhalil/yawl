package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/behzodhalil/yawl/internal/agekey"
	"github.com/behzodhalil/yawl/internal/server/selfbackup"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

var restorePrepared func(stagedDB string)

type RestoreOpts struct {
	Data, Etc                                      string
	Endpoint, Bucket, Region, Access, Secret, From string
	Insecure                                       bool
	Unverified                                     bool
	Store                                          selfbackup.Store
}

func Restore(ctx context.Context, o RestoreOpts, in io.Reader, out io.Writer) error {
	dbPath := filepath.Join(o.Data, "yawl.db")
	leftovers := []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
	for _, n := range selfbackup.EtcFiles {
		leftovers = append(leftovers, filepath.Join(o.Etc, n))
	}
	for _, p := range leftovers {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s already exists. Restore runs on a new machine; move it away first.", p)
		}
	}
	r := bufio.NewReader(in)
	st := o.Store
	if st == nil {
		if o.Secret == "" {
			s, err := secret(r, in, out, "Storage secret key: ", "YAWL_STORAGE_SECRET")
			if err != nil {
				return err
			}
			o.Secret = s
		}
		var err error
		if st, err = selfbackup.S3(o.Endpoint, o.Bucket, o.Region, o.Access, o.Secret, o.Insecure); err != nil {
			return err
		}
	}
	text, err := secret(r, in, out, "Recovery key: ", "YAWL_RECOVERY_KEY")
	if err != nil {
		return err
	}
	id, _, err := agekey.FromRecovery(text)
	if err != nil {
		return err
	}
	sign, err := agekey.BackupSigningKey(text)
	if err != nil {
		return err
	}
	get := func(key string) ([]byte, error) {
		obj, err := st.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", key, err)
		}
		defer obj.Close()
		return io.ReadAll(obj)
	}
	// Anyone who can write the bucket can encrypt to the public recovery key:
	// only a backup signed with the key derived from it is taken (run-1).
	key, enc := o.From, []byte(nil)
	if key == "" {
		keys, err := st.List(ctx, selfbackup.Prefix)
		if err != nil {
			return fmt.Errorf("list %s: %w", o.Bucket, err)
		}
		if len(keys) == 0 {
			return fmt.Errorf("No panel backup in %s under %s.", o.Bucket, selfbackup.Prefix)
		}
		for i := len(keys) - 1; i >= 0 && enc == nil; i-- {
			if !selfbackup.Signed(keys[i]) {
				fmt.Fprintf(out, "Skipped %s: it is not signed.\n", keys[i])
				continue
			}
			b, err := get(keys[i])
			if err != nil {
				return err
			}
			if !selfbackup.Valid(sign, keys[i], b) {
				fmt.Fprintf(out, "Skipped %s: its signature does not match this recovery key.\n", keys[i])
				continue
			}
			key, enc = keys[i], b
		}
		if enc == nil {
			return fmt.Errorf("No panel backup in %s is signed with this recovery key. To restore an unsigned one, name it: --from <key> --unverified.", o.Bucket)
		}
	} else {
		if enc, err = get(key); err != nil {
			return err
		}
		switch {
		case selfbackup.Valid(sign, key, enc):
		case selfbackup.Signed(key):
			return fmt.Errorf("%s is not signed with this recovery key: this panel did not make it, or it was changed in storage.", key)
		case !o.Unverified:
			return fmt.Errorf("%s is not signed, so nothing shows this panel made it. Add --unverified to restore it anyway.", key)
		default:
			sum := sha256.Sum256(enc)
			fmt.Fprintf(out, "%s is not signed: anyone who can write to %s could have made it, with a CA and an owner of their own.\nIts SHA-256 is %x.\nType its key to restore it: ", key, o.Bucket, sum)
			if line, _ := r.ReadString('\n'); strings.TrimSpace(line) != key {
				return errors.New("Not confirmed; nothing was restored.")
			}
		}
	}
	for _, d := range []string{o.Data, o.Etc} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	adopt := func(staged string) error {
		s, err := store.Open(staged)
		if err != nil {
			return err
		}
		defer s.Close()
		for k, v := range map[string]string{"adopt.pending": "1", "adopt.since": strconv.FormatInt(time.Now().UnixMilli(), 10)} {
			if err := s.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: v}); err != nil {
				return err
			}
		}
		if restorePrepared != nil {
			restorePrepared(staged)
		}
		return nil
	}
	if err := selfbackup.Unpack(bytes.NewReader(enc), id, o.Data, o.Etc, adopt); err != nil {
		return err
	}
	fmt.Fprintf(out, "Restored the panel from %s. Start yawl-server: it opens in adopt mode.\n", key)
	return nil
}

func secret(r *bufio.Reader, in io.Reader, out io.Writer, prompt, env string) (string, error) {
	if v := os.Getenv(env); v != "" {
		return strings.TrimSpace(v), nil
	}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(out, prompt)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(out)
		return strings.TrimSpace(string(b)), err
	}
	line, err := r.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", fmt.Errorf("read %s: %w", strings.TrimSuffix(prompt, ": "), err)
	}
	return strings.TrimSpace(line), nil
}

func restoreCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("server restore", flag.ContinueOnError)
	var o RestoreOpts
	fs.StringVar(&o.Data, "data", "/var/lib/yawl", "state directory")
	fs.StringVar(&o.Etc, "etc", "/etc/yawl", "key directory")
	fs.StringVar(&o.Endpoint, "endpoint", "", "S3 endpoint host (http:// for a private store)")
	fs.StringVar(&o.Bucket, "bucket", "", "bucket the backups are in")
	fs.StringVar(&o.Region, "region", "", "region, if the provider has one")
	fs.StringVar(&o.Access, "access-key", "", "access key")
	fs.StringVar(&o.From, "from", "", "object key (default: the newest signed yawl-server/ backup)")
	fs.BoolVar(&o.Unverified, "unverified", false, "restore the unsigned backup --from names, after confirming it")
	fs.BoolVar(&o.Insecure, "insecure", false, "plain http to the store")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.Unverified && o.From == "" {
		return errors.New("--unverified needs --from: an unsigned backup is never chosen for you")
	}
	if o.Endpoint == "" || o.Bucket == "" || o.Access == "" {
		return errors.New("usage: yawl server restore --endpoint E --bucket B --access-key A [--region R] [--from key [--unverified]]")
	}
	return Restore(ctx, o, os.Stdin, os.Stdout)
}
