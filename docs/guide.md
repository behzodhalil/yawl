# Yawl guide

How to use and run Yawl, past the first deploy. Start with the [README](../README.md).

- [Everyday commands](#everyday-commands)
- [Deploying](#deploying)
- [Data](#data)
- [Watching and alerts](#watching-and-alerts)
- [Servers and network](#servers-and-network)
- [Teams and access](#teams-and-access)
- [Moving in](#moving-in)
- [Updating](#updating)
- [Restoring the panel](#restoring-the-panel)
- [Development](#development)

## Everyday commands

- `yawl status` lists services and their last deploy. `yawl deploy <service>` deploys one and follows its steps.
- `yawl rollback api` puts the previous image back. It never touches the database: a release command's changes stay, and its snapshot is kept on the server.
- `yawl logs api -f -q error` tails or searches what Docker keeps on the server, with secret values shown as ••••. Nothing is stored centrally. `--errors` shows only stderr lines.
- `yawl domains api` shows each domain's DNS and certificate state, and the record to set when DNS is wrong. Once a domain has both, `yawl status` and the dashboard show it as the service's URL instead of the free sslip.io one.
- `yawl env set|ls|rm|reveal|diff` manages sealed variables.
- After a deploy goes live, Yawl probes it every 30 seconds. Three failures in the first five minutes roll it back by itself; later they are a "service down" message. Disk at 85% and 95%, and an agent offline for 2 minutes, are messages too.

## Deploying

**Builds.** A repository with a Dockerfile builds from it; Go, Node and Python build with Yawl's own builder; every other language builds with Railpack (`builder: railpack` asks for it always).

**Prebuilt images.** A web service, a worker or a cron job can run an image instead of a repository: `image: grafana/grafana:11.6` in `yawl.yaml`, or *From an image* when you add a service. It deploys only when you press Deploy, and each deploy keeps the digest it pulled, so a rollback brings back that exact image. Private registries need a login in Settings, Registries.

**Templates.** *New service, From a template* installs ready software into an environment: Uptime Kuma, Grafana and Loki, Umami, n8n, Ghost, Gitea, Plausible, Metabase, Vaultwarden and others, 13 in all. Each comes with its volumes and memory limits; passwords and keys are made and sealed in your browser. The list ships with Yawl; nothing is fetched to install one.

**Deploy controls.** A push whose head commit says `[skip ci]`, `[ci skip]`, `[skip deploy]` or `[skip yawl]` deploys nothing. `watch: ["api/**", "go.mod"]` on a service deploys it only when a changed file matches (a push with too many commits to list deploys anyway). A deploy can be canceled while it is queued, building or pulling; the live version is untouched. *Deploy without cache* (or `yawl deploy --no-cache`) builds every step again.

**Start, stop, restart.** *Restart* on a service's page (or `yawl restart <service>`) runs the live version again in a new container, without a build; a web service has no downtime. *Stop* takes it off until *Start*: its data, variables and backups stay, and it is not reported down while stopped. Developers and Admins may.

**Preview environments.** Put `preview: true` in an environment's `yawl.yaml` and every pull request gets its own copy, `pr-<number>`, on the same server: the PR's branch, empty databases its migrations fill, the same variables, its URL posted on the PR. At most 5 run at once; a closed PR, or 7 days without a push, removes it.

**Cron jobs.** A service of `kind: cron` has a `schedule` (five fields), `timezone`, `command` (an argument list) and `timeout`. The agent runs it on its server, so jobs keep running while the panel is down; the Runs tab shows the last 20 runs and has Run now.

**Limits.** Any service, a database too, can have `cpu: 0.5` and `memory: 512MB` in `yawl.yaml`, or the CPU and Memory fields. A limit takes effect at the next deploy. An app that runs out of its memory limit is restarted and you get one message, closed after an hour without another.

**A web service's address.** On a web service's Settings, *Access* sets a password (HTTP basic auth; Yawl keeps only a hash) and *Show the maintenance page* answers every host with a 503 page; both act at once. In `yawl.yaml`, `www: bare` (or `www`) makes `www.acme.uz` answer a 301 to `acme.uz` when both are in `domains`, and `noindex: true` asks search engines to stay away (previews always do). A domain can carry a path, `acme.uz/api`: requests for `/api` and below go to that service with `/api` taken off and sent in `X-Forwarded-Prefix`, and everything else on `acme.uz` to the service that has the host itself.

**GitHub and GitLab.** `yawl github connect` (or Settings, GitHub) connects a GitHub App for private repositories and a deploy on every push. GitLab.com and self-hosted GitLab connect in Settings with an access token: private clones, a deploy on every push, a preview per merge request.

## Data

**Databases.** Postgres; `kind: valkey` runs Valkey 8 (Redis compatible), kept on disk, backed up nightly with `backup: true`; `kind: mysql` runs MySQL 8.4 LTS, backed up nightly with `mysqldump`; `kind: mongodb` runs MongoDB 8.0 as a replica set of one, backed up nightly with `mongodump`. MariaDB and ClickHouse are there too. Apps reach each by `<name>.url`. A Postgres with `pitr: true` keeps 7 days of changes and restores to any second of them: `yawl db restore <db> --at <time> --into <new>`.

**Volumes.** A web service or a worker can keep directories across deploys: `volumes: [{name: uploads, path: /app/uploads}]` in `yawl.yaml`, or the Volumes field. A service with a volume deploys stop, then start, so two versions never write it at once. Each volume is backed up encrypted every night, like a database, restored in place from the Backups tab, carried along when its environment moves, and removed when the service is purged.

**Move an environment.** On the project page, choose Move next to "staging runs on hel1", pick the server and read the plan. Every service of the environment moves together. Paste the recovery key and apply: the browser re-seals every secret to the new server, and the key never leaves the page.

- **With a database**, the apps stop on the old server first, the database is backed up there and restored on the new one, then the apps start there. The environment is down from the stop to the switch, and no write is lost. Backup storage must be set up.
- **Without one**, the apps start on the new server before the old ones stop.
- **Its free URLs change** with the server's address: `web-staging.<new ip, dashed>.sslip.io`. A custom domain needs its A record pointed at the new server, and the plan names it.
- **When the old server is gone**, the database comes from its last backup, and the plan says how old it is. The old server is cleaned up (containers removed, volumes kept) as soon as it is back online.
- **If a step fails after the switch**, the page shows why, and Retry resumes there. A failure before the switch starts the apps on the old server again.
- **If you use `yawl.yaml`**, change its `server:` to the new server after the move; `yawl apply` refuses a file that still names the old one.
- **Writes from outside the environment** reach the database through `outside_port` and are not stopped by the move. Stop those clients before you move, or their last writes stay on the old server.

**Deleting.** A service, database, environment or project can be deleted from the dashboard (type its name). It stops at once, waits 48 hours in Settings, Trash where Restore brings it back, then is purged with its containers, volume and backups.

## Watching and alerts

**The watcher.** Settings, Watcher deploys a small Cloudflare Worker into your own account. It checks your apps, machines, heartbeats and backups from outside every minute, sends the Telegram alerts itself even when your server is down, and serves the status page you publish on it. It needs a token with two permissions (Workers Scripts: Edit, D1: Edit).

**Metrics.** Each service has a Metrics tab (CPU, memory, requests with 5xx, p95 answer time) and the Servers page shows each machine's last day. Minute detail is kept 7 days, hour detail 90 days. Yawl warns a week before a disk fills, and when a machine's memory stays above 90% for 10 minutes.

**Slack, Discord and email.** Settings, Slack, Discord, email sends every alert Telegram gets to an incoming Slack webhook, a Discord channel webhook and an SMTP mailbox as well. Each channel is retried on its own.

## Servers and network

**More servers.** Servers, Add server in the panel shows a command to run on the new machine.

**The private network.** Every server joins one WireGuard network as its agent joins, with nothing to set up but UDP 51820 open between servers. An app can use a database in another environment of its project, on another server, with `--ref production/acme-db.url`: the database listens only on the private address, over TLS. The Servers page shows each server's peers and their last handshake, and Telegram says when two servers stop reaching each other.

**Firewall.** Servers, a server, Firewall turns on a firewall Yawl writes and keeps: everything coming in closed except SSH, web traffic, the private network, Postgres outside ports and your own rules, Docker's published ports included. A change goes back by itself unless you press Keep within 2 minutes. Repeated SSH failures ban an address for an hour; the page lists every listening port and who can reach it.

**Your own panel domain.** Settings, Panel gives the panel your own domain. Once its DNS is right and its certificate is issued, the free `panel.<ip>.sslip.io` host is turned off; it comes back by itself if the domain is cleared or stops working. It stays on while `--public-url` names it, since agents on other servers dial that address: change `--public-url` to the domain and run `yawl agent server-url https://<domain>` on each of them first.

## Teams and access

**Members.** Settings, Members invites people by a one-time link (7 days) as Admin, Developer (deploys and edits variables; cannot reveal secrets or manage servers, though code they deploy runs with the variables) or Viewer (reads only). The server checks the role on every request; API tokens carry their maker's role.

**Audit log.** Settings, Audit log lists every change made through the panel or the API, who made it and when, kept a year, with a CSV export.

**Two-step sign in.** Settings, Account turns on a code from an authenticator app after the password, with ten one-time recovery codes. An Admin can make it required for everyone, and can turn it off for a member who lost their phone; `yawl server reset-2fa <email>` on the panel's machine is the last way back. A forgotten password has the same kind of way back: `yawl server reset-password <email>` prints a link to a new password, good once for an hour.

**For an AI assistant.** `yawl mcp` serves the Model Context Protocol: an assistant can read services, deploys and logs, deploy and roll back, with your CLI login's role.

## Moving in

- `yawl compose import` turns a `docker-compose.yml` into Yawl services: database images become Yawl databases with backups, app URLs to them become references.
- `yawl import render|fly|caprover|kamal|dokku` reads `render.yaml`, `fly.toml`, `captain-definition`, Kamal's `config/deploy.yml`, or what Dokku's `config:export` prints, and does what `yawl compose import` does. Secrets kept on the platform are named, never guessed.
- *New service, Import an app* takes the GitHub repository of an app made with Lovable, Bolt, v0 or Replit. Yawl says who made it and how it will run: a Vite site is built and served as files, a Next.js or Replit app runs its own server. `VITE_*` keys come ticked as build time. A Supabase backend stays where it is.

## Updating

`yawl update` (or Settings, Updates) verifies the new release, swaps the panel's binary and restarts it. Agents see the new version and update themselves. App containers and Caddy are not restarted. A build without a release key (any `dev` build) does not update itself.

Before it replaces itself, the panel takes its own backup (as the nightly one, to the backup storage). If that backup fails, the update stops and says why; *Update without a backup* goes on anyway. With no backup storage yet, it updates and says it was not backed up first.

Releases are served from `https://useyawl.com/releases`; `YAWL_RELEASES` (or `--release-base`) points the installer and `yawl update` at a mirror. How releases are signed is in [SECURITY.md](../SECURITY.md).

## Restoring the panel

The panel backs itself up every night into the backup storage, encrypted to the recovery key: its database and the keys in `/etc/yawl`. Each backup is also signed, with a key derived from the recovery key that the panel was given when the recovery key was made. Anyone who can write to the bucket can encrypt to the recovery key's public half, so restore takes only a backup with a valid signature. A panel whose recovery key was made before signing existed makes unsigned backups until you run `yawl recovery-key sign-backups` once (it asks for the recovery key, checks it is this panel's, and keeps it on your machine).

When the machine it ran on is gone:

1. Install Yawl on a new machine and ignore the claim link it prints.
2. Stop both services and move the fresh state aside: `systemctl stop yawl-server yawl-agent`, then move `/var/lib/yawl/yawl.db*`, `/etc/yawl` and `/var/lib/yawl/agent` somewhere else. Restore refuses to write over any of them.
3. Run `yawl server restore --endpoint s3.example.com --bucket acme --access-key KEY`. It asks for the storage secret and the recovery key, then restores the newest backup signed with that recovery key and names each newer one it skipped (*not signed*, or *its signature does not match this recovery key*). A skipped backup you did not expect means someone else wrote to the bucket.
   - `--from yawl-server/<key>` restores that backup, only if its signature matches.
   - An unsigned backup restores only with `--from <key> --unverified`: restore prints its SHA-256 and asks you to type its key back. Nothing shows the panel made it, so use this only for a backup from before signing that you have reason to trust.
4. Start the server, then join this machine's agent to the restored panel again:

   ```
   systemctl start yawl-server
   yawl server join-token --panel --public-url http://127.0.0.1:7000
   yawl agent join http://127.0.0.1:7000 <token from the line above> --ip <this machine's IP>
   systemctl start yawl-agent
   ```

5. Open `https://panel.<new ip, dashed>.sslip.io`, sign in, and choose Review. The panel is in adopt mode and changes nothing until you accept. Choose "Accept without <old server>" for the machine that is gone. Its apps need a new home: move their environment.

Agents on other machines reconnect by themselves when the panel keeps its address (your own panel domain, repointed). If the address changed, run `yawl agent server-url https://panel.acme.uz` on each of them and restart that agent.

## Development

`make web` builds the dashboard (Node 22 or newer) into `web/dist`, and `go build` embeds it in the binary. Without a build, the panel says so and the API and CLI work as before. For work on the pages, run `yawl server` and `cd web && npm run dev`: Vite proxies `/api` to `127.0.0.1:7000`.

Tests, and how to send a change, are in [CONTRIBUTING.md](../CONTRIBUTING.md). Security problems go through [SECURITY.md](../SECURITY.md).
