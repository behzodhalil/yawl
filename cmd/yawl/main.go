package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/behzodhalil/yawl/internal/agent"
	"github.com/behzodhalil/yawl/internal/cli"
	"github.com/behzodhalil/yawl/internal/cloud"
	"github.com/behzodhalil/yawl/internal/server"
)

var version = "dev"

const usage = `usage: yawl <command>

  server      run the control plane (server claim-link: a new first-run link;
              server reset-2fa <email>: two-step sign in off for one person;
              server reset-password <email>: a one-time link to a new password)
  agent       run the agent on this machine
  login       store the server URL and an API token
  apply       show the plan for yawl.yaml, then apply it
  deploy      deploy a service and follow its steps
  status      list services and their last deploy
  rollback    put the previous version back (never touches the database)
  restart     run the live version again in a new container, no build
  stop, start take a service off until it is started again
  logs        tail or search a service's logs (-f, -q text, --errors)
  domains     show a service's domains and their DNS and certificate state
  env         set, ls, rm, reveal or diff sealed variables
  recovery-key init   make the recovery key (once)
  recovery-key sign-backups  let an older panel sign its own backups (asks for the recovery key)
  github connect      connect a GitHub App (push to deploy, private repos)
  db          backup, backups or restore a database (restore --at: to a time)
  compose import      turn docker-compose.yml into Yawl services
  import      the same from render.yaml, fly.toml, captain-definition, Kamal's
              config/deploy.yml or Dokku's config:export (render|fly|caprover|kamal|dokku)
  mcp         serve the Model Context Protocol on stdio, for an AI assistant
  skills add claude|cursor|codex  teach an AI agent Yawl, in this repository
  update      move the panel to a new release (agents follow; apps keep running)
  cloud       run Yawl Cloud's front (env YAWL_CLOUD_*)
  version     print the version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	role, args := os.Args[1], os.Args[2:]
	var err error
	switch role {
	case "server":
		err = server.Run(ctx, args, version)
	case "agent":
		err = agent.Run(ctx, args, version)
	case "cloud":
		err = cloud.Run(ctx, args)
	case "run":
		if os.Getenv("YAWL_ROLE") == "front" {
			err = cloud.Run(ctx, args)
		} else {
			err = server.Run(ctx, append([]string{"--data", "/data", "--etc", "/data/etc"}, args...), version)
		}
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		if err = cli.Run(ctx, role, args); errors.Is(err, cli.ErrUnknown) {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "yawl:", err)
		os.Exit(server.ExitCode(err))
	}
}
