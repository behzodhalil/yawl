package cli

import (
	"context"
	"errors"
	"fmt"
)

func githubCmd(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "connect" {
		return errors.New("usage: yawl github connect")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	var out struct {
		URL        string `json:"url"`
		Connected  bool   `json:"connected"`
		InstallURL string `json:"install_url"`
	}
	if err := c.do(ctx, "POST", "/api/github/connect", nil, &out); err != nil {
		return err
	}
	if out.Connected {
		fmt.Fprintln(stdout, "GitHub is connected. To give Yawl more repositories: "+out.InstallURL)
		return nil
	}
	fmt.Fprintln(stdout, "Open this link in your browser within 10 minutes:")
	fmt.Fprintln(stdout, out.URL)
	return nil
}
