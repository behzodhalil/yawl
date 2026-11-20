package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/behzodhalil/yawl/internal/agekey"
	"github.com/behzodhalil/yawl/internal/seal"
	"github.com/behzodhalil/yawl/internal/spec"
)

func env(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: yawl env set|ls|rm|reveal|diff ...")
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("env "+sub, flag.ContinueOnError)
	file := fs.String("f", "yawl.yaml", "spec file naming the project and environment")
	shared := fs.Bool("shared", false, "a variable for every service in the environment")
	recovery := fs.Bool("recovery", false, "open with the recovery key instead of asking the agent")
	buildTime := fs.Bool("build", false, "also give the value to the build, as a BuildKit secret")
	ref := fs.String("ref", "", "refer to a database's URL (acme-db.url, or production/acme-db.url in another environment) instead of a value")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	f, err := spec.Parse(b)
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	svcPath := func(svc string) string { return fmt.Sprintf("/api/services/%s/%s/%s", f.Project, f.Environment, svc) }
	varPath := func(svc, key string) string {
		if *shared {
			return fmt.Sprintf("/api/environments/%s/%s/env/%s", f.Project, f.Environment, key)
		}
		return svcPath(svc) + "/env/" + key
	}

	switch {
	case sub == "set" && len(pos) == 2 && *ref != "":
		body, _ := json.Marshal(map[string]string{"ref": *ref})
		var out struct{ Note string }
		if err := c.do(ctx, "PUT", varPath(pos[0], pos[1]), body, &out); err != nil {
			return err
		}
		if out.Note != "" {
			fmt.Fprintln(stdout, out.Note)
		}
		fmt.Fprintf(stdout, "%s now refers to %s. Takes effect on the next deploy: yawl deploy %s\n", pos[1], *ref, pos[0])
		return nil

	case sub == "set" && len(pos) == 2:
		key, value, inline := strings.Cut(pos[1], "=")
		if inline {
			fmt.Fprintln(os.Stderr, "Note: a value on the command line stays in your shell history.")
		} else if value, err = readValue(key); err != nil {
			return err
		}
		agentBox, recBox, err := sealValue(ctx, c, svcPath(pos[0]), value)
		if err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]any{"agent_box": agentBox, "recovery_box": recBox, "build": *buildTime})
		if err := c.do(ctx, "PUT", varPath(pos[0], key), body, nil); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Set %s. Takes effect on the next deploy: yawl deploy %s\n", key, pos[0])
		return nil

	case sub == "rm" && len(pos) == 2:
		if err := c.do(ctx, "DELETE", varPath(pos[0], pos[1]), nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed %s. Takes effect on the next deploy: yawl deploy %s\n", pos[1], pos[0])
		return nil

	case sub == "ls" && len(pos) == 1:
		var rows []struct {
			Key, Scope, Ref string
			Build           bool
		}
		if err := c.do(ctx, "GET", svcPath(pos[0])+"/env", nil, &rows); err != nil {
			return err
		}
		for _, r := range rows {
			line := fmt.Sprintf("%-32s%s", r.Key, r.Scope)
			if r.Build {
				line += "  build"
			}
			if r.Ref != "" {
				line += "  → " + r.Ref
			}
			fmt.Fprintln(stdout, line)
		}
		return nil

	case sub == "reveal" && len(pos) == 2:
		var rows []struct{ Key, Ref string }
		if err := c.do(ctx, "GET", svcPath(pos[0])+"/env", nil, &rows); err == nil {
			for _, r := range rows {
				if r.Key == pos[1] && r.Ref != "" {
					return fmt.Errorf("%s is a reference to %s; reveal the database's URL with: yawl env reveal %s URL", r.Key, r.Ref, strings.TrimSuffix(r.Ref, ".url"))
				}
			}
		}
		var value []byte
		if *recovery {
			line, err := readHidden("Recovery key: ")
			if err != nil {
				return err
			}
			pub, priv, err := seal.DecodeRecovery(line)
			if err != nil {
				return err
			}
			var out struct{ Box string }
			if err := c.do(ctx, "GET", svcPath(pos[0])+"/env/"+pos[1]+"/recovery_box", nil, &out); err != nil {
				return err
			}
			if value, err = seal.Open(pub, priv, out.Box); err != nil {
				return err
			}
		} else {
			pub, priv, err := seal.GenerateKey()
			if err != nil {
				return err
			}
			body, _ := json.Marshal(map[string]string{"to": pub.String()})
			var out struct{ Box string }
			if err := c.do(ctx, "POST", svcPath(pos[0])+"/env/"+pos[1]+"/reveal", body, &out); err != nil {
				return err
			}
			if value, err = seal.Open(pub, priv, out.Box); err != nil {
				return err
			}
		}
		fmt.Fprintln(stdout, string(value))
		return nil

	case sub == "diff" && len(pos) == 2:
		a, err := envKeys(ctx, c, f.Project, pos[0])
		if err != nil {
			return err
		}
		b, err := envKeys(ctx, c, f.Project, pos[1])
		if err != nil {
			return err
		}
		same := true
		for _, side := range []struct {
			name      string
			have, not []string
		}{{pos[0], a, b}, {pos[1], b, a}} {
			for _, k := range side.have {
				if !slices.Contains(side.not, k) {
					fmt.Fprintf(stdout, "only in %s: %s\n", side.name, k)
					same = false
				}
			}
		}
		if same {
			fmt.Fprintln(stdout, "Both have the same keys. (Values are not compared: the server cannot read them.)")
		}
		return nil
	}
	return errors.New("usage: yawl env set <service> KEY [--shared] | ls <service> | rm <service> KEY [--shared] | reveal <service> KEY [--recovery] | diff <env> <env>")
}

func envKeys(ctx context.Context, c *client, project, env string) ([]string, error) {
	var rows []struct{ Key, Scope, Service string }
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/environments/%s/%s/env", project, env), nil, &rows); err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rows {
		if r.Service != "" {
			out = append(out, r.Service+"/"+r.Key)
		} else {
			out = append(out, r.Key)
		}
	}
	return out, nil
}

func sealTo(pubkey, value string) (string, error) {
	pk, err := seal.ParsePublicKey(pubkey)
	if err != nil {
		return "", err
	}
	return seal.Seal(pk, []byte(value))
}

func readValue(key string) (string, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprintf(os.Stderr, "Value for %s: ", key)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	b, err := io.ReadAll(io.LimitReader(stdin, seal.MaxValue+3))
	if err != nil {
		return "", err
	}
	v := string(b)
	if strings.HasSuffix(v, "\r\n") {
		v = strings.TrimSuffix(v, "\r\n")
	} else {
		v = strings.TrimSuffix(v, "\n")
	}
	if len(v) > seal.MaxValue {
		return "", fmt.Errorf("the value is over %d bytes", seal.MaxValue)
	}
	return v, nil
}

func recoveryKey(ctx context.Context, args []string) error {
	if len(args) != 1 || (args[0] != "init" && args[0] != "sign-backups") {
		return errors.New("usage: yawl recovery-key init | sign-backups")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	if args[0] == "sign-backups" {
		return signBackups(ctx, c)
	}
	pub, priv, err := seal.GenerateKey()
	if err != nil {
		return err
	}
	text := seal.EncodeRecovery(priv)
	k, err := agekey.BackupSigningKey(text)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"pubkey": pub.String(), "backup_key": base64.StdEncoding.EncodeToString(k)})
	if err := c.do(ctx, "PUT", "/api/recovery-key", body, nil); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Your recovery key:")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "  "+text)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Store this offline. Yawl cannot show it again.")
	fmt.Fprintln(stdout, "It opens every secret if a server is lost; anyone holding it can too.")
	return nil
}

func signBackups(ctx context.Context, c *client) error {
	text, err := readHidden("Recovery key: ")
	if err != nil {
		return err
	}
	_, pub, err := agekey.FromRecovery(text)
	if err != nil {
		return err
	}
	var st struct {
		Pubkey string `json:"pubkey"`
	}
	if err := c.do(ctx, "GET", "/api/recovery-key", nil, &st); err != nil {
		return err
	}
	if st.Pubkey != base64.StdEncoding.EncodeToString(pub[:]) {
		return errors.New("That is not this panel's recovery key.")
	}
	k, err := agekey.BackupSigningKey(text)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"key": base64.StdEncoding.EncodeToString(k)})
	if err := c.do(ctx, "PUT", "/api/recovery-key/backup-key", body, nil); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "The panel signs its own backups from the next one on (Settings > Panel backup > Back up now makes one).")
	return nil
}

func readHidden(prompt string) (string, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

func sealValue(ctx context.Context, c *client, svcPath, value string) (agentBox, recBox string, err error) {
	var keys struct {
		AgentPubkey    string `json:"agent_pubkey"`
		RecoveryPubkey string `json:"recovery_pubkey"`
		Server         string `json:"server"`
	}
	if err := c.do(ctx, "GET", svcPath+"/keys", nil, &keys); err != nil {
		return "", "", err
	}
	if keys.AgentPubkey == "" {
		return "", "", fmt.Errorf("server %s has no secrets key; re-join it", keys.Server)
	}
	if keys.RecoveryPubkey == "" {
		return "", "", errors.New("no recovery key yet; run `yawl recovery-key init` first")
	}
	if agentBox, err = sealTo(keys.AgentPubkey, value); err != nil {
		return "", "", err
	}
	recBox, err = sealTo(keys.RecoveryPubkey, value)
	return agentBox, recBox, err
}
