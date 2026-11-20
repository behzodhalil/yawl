package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"
)

var updatePoll = 2 * time.Second

func updateCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	version := fs.String("version", "", "release to move to (default: the latest)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"version": strings.TrimPrefix(*version, "v")})
	var out struct{ Version string }
	if err := c.do(ctx, "POST", "/api/update", body, &out); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Updating to v%s. The panel restarts; apps keep running.\n", out.Version)
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(updatePoll)
		var st struct{ Current string }
		if c.do(ctx, "GET", "/api/update", nil, &st) == nil && st.Current == out.Version {
			fmt.Fprintf(stdout, "The panel runs v%s.\n", out.Version)
			return nil
		}
	}
	return errors.New("the panel did not come back on the new version within 2 minutes; check `journalctl -u yawl-server`")
}
