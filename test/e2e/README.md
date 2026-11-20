# End-to-end acceptance tests

`make e2e` runs these inside a `golang` container on the Docker host's own
network and PID namespace, so Caddy, container IPs and `/proc` are real.
Ports 80 and 443 on the Docker host must be free.

| Test | Design record |
|---|---|
| `TestE2EGoodDeployZeroFailures` | acceptance 3: zero failed requests across a switch |
| `TestE2ECrashKeepsOld` | acceptance 2: a crashing commit keeps the old version live, Telegram names the cause |
| `TestE2ELocalhostCause` | §9.2: an app bound to 127.0.0.1 is named as such |
| `TestE2EServerDownAppsUp` | acceptance 6: apps answer with the server stopped; a restart redeploys nothing |
| `TestE2ESecretReachesApp` | §10: a value sealed by the CLI is opened by the agent and reaches the app |
| `TestE2EPlantedValueAbsent` | acceptance 7: the value is not in `yawl.db`, its WAL, or the server log |
| `TestE2ECrashMasksSecret` | §10: a secret the app prints shows as •••• in the cause and on Telegram |
| `TestE2EBackupRestore` | acceptance 4: last night's backup restored into a new database, the app switched to it |
| `TestE2EReleaseAndRollback` | §9: release after snapshot; a failing release keeps the old version (0 failed requests); rollback names the snapshot |
| `TestE2EAutoRollback` | §9 "After live": a version failing 3 probes in its first 5 minutes is rolled back by itself, with a message |
| `TestE2ELogsSearchMasked` | §14: `yawl logs -q` searches on the agent and masks secret values |
| `TestE2EDomainWrongDNS` | §12: a domain pointing elsewhere is shown with the record to set and is not routed |
| `TestE2EDashboardFlow` | §3 and day one, through the dashboard's own requests: claim, recovery key, project, Inspect, plan, apply, a browser-sealed key, deploy; the panel serves its pages |

| `TestE2EServerRestoreAdopt` | §17: the panel restored from its own backup on a "new machine" opens in adopt mode, sees the agent runs a newer deploy, acts on nothing until accepted; 0 failed requests throughout |
| `TestE2EPanelHost` | §11/§12: the panel answers on `panel.<ip>.sslip.io` through its own agent, with its security headers |

The acceptance run on a real VPS (1, 5, 6, 7, 8) is `make hetzner` (needs `HCLOUD_TOKEN`; it creates and deletes a Hetzner server).

`YAWL_E2E_HOLD=1 make e2e E2E_RUN=TestE2EDashboardFlow E2E_TIMEOUT=60m` leaves that stack running after the
test and prints the panel URL and the owner's sign-in, for a walk-through in a browser.

## Last run

2026-10-24, Plan 7 (install and operations); Docker Desktop 29.1.2 (linux/arm64 VM), Go 1.27.3:

```
    deploy_test.go:280: 348 requests, 0 failed
--- PASS: TestE2EGoodDeployZeroFailures (11.98s)
--- PASS: TestE2ECrashKeepsOld (21.90s)
--- PASS: TestE2ELocalhostCause (10.35s)
--- PASS: TestE2EServerDownAppsUp (10.74s)
--- PASS: TestE2ESecretReachesApp (3.29s)
--- PASS: TestE2EPlantedValueAbsent (8.96s)
--- PASS: TestE2EDetectedGo (13.79s)
--- PASS: TestE2EBuildSecret (4.24s)
--- PASS: TestE2ECrashMasksSecret (5.24s)
    deploy_test.go:655: 193 requests, 0 failed
--- PASS: TestE2EBackupRestore (62.07s)
--- PASS: TestE2EMissedBackupMessage (35.53s)
--- PASS: TestE2EReleaseAndRollback (46.12s)
--- PASS: TestE2EAutoRollback (126.72s)
--- PASS: TestE2ELogsSearchMasked (3.02s)
--- PASS: TestE2EDomainWrongDNS (60.75s)
--- PASS: TestE2EDashboardFlow (5.01s)
    plan7_test.go:144: 168 requests, 0 failed
--- PASS: TestE2EServerRestoreAdopt (21.73s)
--- PASS: TestE2EPanelHost (0.44s)
ok  	github.com/behzodhalil/yawl/test/e2e	451.915s
```
