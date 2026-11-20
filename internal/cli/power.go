package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
)

func powerCmd(ctx context.Context, verb string, args []string) error {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	file := fs.String("f", "yawl.yaml", "spec file naming the project and environment")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: yawl %s <service>", verb)
	}
	f, err := readSpec(*file)
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	var out struct {
		DeployID string `json:"deploy_id"`
		Note     string `json:"note"`
	}
	if err := c.do(ctx, "POST", fmt.Sprintf("/api/services/%s/%s/%s/%s", f.Project, f.Environment, pos[0], verb), nil, &out); err != nil {
		return err
	}
	if verb == "stop" {
		fmt.Fprintf(stdout, "Stopped %s. Start it again: yawl start %s\n", pos[0], pos[0])
		if out.Note != "" {
			fmt.Fprintln(stdout, out.Note)
		}
		return nil
	}
	if out.DeployID == "" {
		return errors.New("the server queued no deploy")
	}
	return follow(ctx, c, out.DeployID)
}
