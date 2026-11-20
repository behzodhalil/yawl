package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"
)

func domainsCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("domains", flag.ContinueOnError)
	file := fs.String("f", "yawl.yaml", "spec file naming the project and environment")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: yawl domains <service>")
	}
	f, err := readSpec(*file)
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	var ds []struct {
		Host      string `json:"host"`
		DNS       string `json:"dns"`
		DNSDetail string `json:"dns_detail"`
		Cert      string `json:"cert"`
	}
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/services/%s/%s/%s/domains", f.Project, f.Environment, pos[0]), nil, &ds); err != nil {
		return err
	}
	if len(ds) == 0 {
		fmt.Fprintln(stdout, "No custom domains. Add them under domains: in yawl.yaml.")
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tDNS\tCERTIFICATE\tDETAIL")
	for _, d := range ds {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.Host, d.DNS, d.Cert, d.DNSDetail)
	}
	return w.Flush()
}
