# Yawl

Yawl deploys apps and databases to the user's own servers. A panel holds the state; an agent on each server runs the containers. You work through the `yawl` CLI, logged in with `yawl login`, or through the `yawl mcp` tools. Both act with the role of the user's token: a Viewer reads and cannot deploy, and every action lands in the panel's Activity.

## Rules

- Read before you change code. Run `yawl status`, then `yawl logs <service> -n 200` (add `--errors` for stderr, `-q <text>` to search) and the last deploy's steps (`yawl deploy` prints them; the MCP `deploy_log` tool takes a deploy id from `deploys`). Fix the cause the logs show, not a guess.
- Never print a secret value. Do not run `yawl env reveal`, do not echo variables, do not paste `.env` files into output. `yawl env ls` shows names only, and logs show secret values as ••••.
- Never guess a server, project or environment. Take them from `yawl.yaml` (`server`, `project`, `environment`) or ask the user. A name that is not in `yawl status` does not exist.
- Change `yawl.yaml`, then run `yawl apply` and read the plan before saying yes. `yawl apply` never deletes a service or a database.
- Deploy with `yawl deploy <service>` and follow it to the end. A deploy fails when the new version does not pass its health check, and its log names the cause: a crash, the wrong port, a bind to localhost, a timeout. After a deploy goes live, three failed probes in the first five minutes roll it back by itself. If the user's app is broken after a deploy, `yawl rollback <service>` puts the previous version back. A rollback never touches the database.
- Ask the user before `yawl db restore`, `yawl stop`, `yawl update`, moving an environment, or anything else that takes a service off or replaces data.

## Commands

- `yawl status`: every service and its last deploy.
- `yawl apply [-f yawl.yaml] [--yes]`: show the plan for `yawl.yaml`, then apply it.
- `yawl deploy <service> [--commit <sha>] [--no-cache]`, `yawl deploy --tag <tag>`: deploy and follow the steps.
- `yawl rollback <service> [--to <deploy id>]`: the previous version back.
- `yawl restart|stop|start <service>`: run the live version again, or take it off until started.
- `yawl logs <service> [-f] [-q text] [-n 200] [--errors]`: tail or search what Docker keeps on the server, at most 1000 lines.
- `yawl domains <service>`: each domain's DNS and certificate state, and the record to set.
- `yawl env set|ls|rm|diff`: sealed variables. `--ref <db>.url` refers to a database's URL instead of a value.
- `yawl db backup|backups|restore|import <database>`: database backups.
- `yawl export <project>/<environment> > yawl.yaml`: write the file for what runs now.
- `yawl compose import`, `yawl import render|fly|caprover|kamal|dokku`: turn another tool's file into Yawl services.

## MCP tools

`yawl mcp` serves these on stdio (`claude mcp add yawl -- yawl mcp`):

- Reads: `list_services`, `service`, `deploys`, `deploy_log`, `logs` (up to 1000 lines, `search` filters), `incidents`.
- Changes: `deploy` (the branch's head, or a commit), `rollback`.
- It cannot read a variable's value, delete, restore, or apply `yawl.yaml`.

## yawl.yaml

One file per environment.

```yaml
project: acme
environment: production
server: hel1
preview: true
services:
  - name: web
    kind: web
    repo: https://github.com/acme/web
    branch: main
    port: 3000
    domains: [acme.uz, www.acme.uz]
    www: bare
  - name: db
    kind: postgres
    version: 17
```

Environment fields:

- `project`, `environment`, `server`: lowercase letters, digits and dashes. `server` names a server joined to the panel.
- `preview`: every pull request gets a copy of this environment, `pr-<number>`, on the same server. At most 5 at once; closed or 7 days without a push removes it.
- `preview_domain`: needs `preview: true`; each preview's web services get `<service>-pr-<n>.<this>`.
- `services`: the list below.

Service fields:

- `name`: lowercase letters, digits and dashes, unique in the file.
- `kind`: `web`, `worker`, `cron`, `postgres`, `valkey`, `mysql`, `mariadb`, `mongodb` or `clickhouse`.
- `repo`: an `https://` or `git@` URL to build from. `image`: a prebuilt image like `grafana/grafana:11` in place of `repo`; never both, and a database has neither.
- `branch`: the branch a push deploys. `auto_deploy`: deploy on a push to `branch`; true unless written false.
- `watch`: paths like `api/**`; a push deploys only when a changed file matches.
- `builder`: `railpack`, or left out. A repo with a Dockerfile builds from it; Go, Node and Python use Yawl's own builder; other languages use Railpack.
- `root`: the folder in the repo the app builds from, like `apps/web`.
- `steps`: an app from a repo with no Dockerfile: `install`, `build` and `start` are one-line commands the build runs in place of the ones Yawl or Railpack picks; `output` (a web service only) is the folder the build writes, served as a static site, and cannot be set together with `start`. A repo with a Dockerfile ignores them.
- `port`: the port a web service listens on. A worker and a database have none.
- `health_path`: starts with `/`. `health_timeout`, `drain_timeout`: durations like `30s`.
- `release`, `release_timeout`: web and worker; an argument list run on the new image before it starts, like a migration (default timeout 10m). A database has none.
- `command`: web, worker and cron; replaces the image's command, as an argument list.
- `domains`: web only; lowercase hosts, or a host with a path like `acme.uz/api`. sslip.io hosts are Yawl's own.
- `www`: web only; `bare` or `www`, the name the other answers a 301 to.
- `noindex`: web only; asks search engines to stay away.
- `replicas`: web and worker; 1 to 10, and 1 when the service has a volume.
- `volumes`: web and worker; up to 4 of `{name, path}`, the path absolute and not `/`. Backed up encrypted nightly.
- `schedule`, `timezone`, `timeout`: cron only; five-field schedule, a time zone name, 1m to 24h (default 1h). A cron job needs a `command`.
- `cpu`: cores, at least 0.1. `memory`: like `512MB`, at least 64MB; MySQL and MariaDB need 256MB, MongoDB 512MB, ClickHouse 1GB.
- `version`: a database's major version (Postgres 16, 17 or 18).
- `backup`: Valkey and ClickHouse; back up nightly. Postgres, MySQL, MariaDB and MongoDB are always backed up.
- `backup_at`: `HH:MM` UTC, for a database or a service with volumes.
- `keep`: `{daily, weekly, monthly}` backups to keep, for a database or a service with volumes.
- `pitr`: Postgres only; 7 days of changes, restorable to any second.
- `outside_port`, `allow`: Postgres only; a port from 1024 to 65535 open to the IPv4 CIDRs in `allow`.
- `preview`: false leaves the service out of previews.
- `tags`: up to 10 labels to filter and deploy by.

Apps reach a database by reference: `yawl env set web DATABASE_URL --ref db.url`.

## The plan

`yawl apply` prints one line a change: `create`, `update`, `unchanged`, or `warn`. It refuses a file that breaks a rule above, with the field and the reason. A service with a volume is down a few seconds while its new version starts; a volume dropped from the file stays on disk until the service is deleted. A change to domains, `www`, `noindex`, `auto_deploy`, `watch`, `tags`, `preview` or backups takes effect without a deploy; any other change needs one.
