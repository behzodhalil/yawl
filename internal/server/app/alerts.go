package app

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/notify"
	"github.com/behzodhalil/yawl/internal/spec"
)

const fail, ok, warn = notify.Fail, notify.OK, notify.Warn

func SvcPath(label string, tab ...string) string {
	p := strings.SplitN(label, "/", 3)
	if len(p) != 3 {
		return ""
	}
	out := "/p/" + url.PathEscape(p[0]) + "/" + url.PathEscape(p[1]) + "/s/" + url.PathEscape(p[2])
	for _, t := range tab {
		out += "/" + t
	}
	return out
}

func serverPath(name string, tab ...string) string {
	return strings.Join(append([]string{"/servers/" + url.PathEscape(name)}, tab...), "/")
}

func envWord(label string) string {
	p := strings.SplitN(label, "/", 3)
	if len(p) < 2 || p[1] == "" {
		return "The"
	}
	return strings.ToUpper(p[1][:1]) + p[1][1:]
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func isSHA(s string) bool {
	return len(s) >= 7 && strings.Trim(s, "0123456789abcdef") == ""
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func svc(label string, state notify.State, head, badge, line, text string) notify.Message {
	m := notify.New(state, head, badge, line, text)
	if p := strings.SplitN(label, "/", 3); len(p) >= 2 {
		m.Project, m.Env = p[0], p[1]
	}
	return m
}

func srv(server string, state notify.State, head, badge, line, text string) notify.Message {
	m := notify.New(state, head, badge, line, text)
	m.Server = server
	return m
}

func open(m notify.Message, label, path string) notify.Message {
	if path == "" {
		return m
	}
	return m.Open(label, path)
}

var stepWords = map[string]string{"build": "Build", "start": "Start", "check": "Health check", "switch": "Route traffic",
	"drain": "Stop old version", "stop_old": "Stop old version", "ensure": "Start database", "restore": "Restore backup",
	"restore_volume": "Restore volume", "snapshot": "Snapshot database", "release": "Release command", "schedule": "Schedule job"}

func stepWord(s string) string {
	if w, ok := stepWords[s]; ok || s == "" {
		return w
	}
	return strings.ToUpper(s[:1]) + strings.ReplaceAll(s[1:], "_", " ")
}

func DeployFailed(label, deployID, commit, step, cause, serving string, rolledBack bool, text string) notify.Message {
	badge := "Failed"
	if rolledBack {
		badge = "Rolled back"
	}
	m := svc(label, fail, "Deployment failed", badge, envWord(label)+" deploy failed", text).
		AddCode("Service", label).AddCode("Commit", short(commit)).Add("Stage", stepWord(step)).
		AddCode("Error", firstLine(cause))
	if isSHA(serving) {
		m = m.AddCode("Still serving", short(serving))
	} else {
		m = m.Add("Still serving", serving)
	}
	m = m.Then("Check the build log.")
	return open(open(m, "Build log", SvcPath(label, "deploys", deployID)), "Open service", SvcPath(label))
}

func DeployLive(label, deployID, commit, took, app, text string) notify.Message {
	m := svc(label, ok, "Deployment live", "Deployed", envWord(label)+" deploy is live", text).
		AddCode("Service", label).AddCode("Commit", short(commit)).Add("Took", took)
	if app != "" {
		m = m.Open("Open app", app)
	}
	return open(m, "Deploy log", SvcPath(label, "deploys", deployID))
}

func AutoRollback(label, to, check, text string) notify.Message {
	m := svc(label, fail, "Deployment failed", "Rolled back", envWord(label)+" deploy rolled back", text).
		AddCode("Service", label).Add("Check", check).AddCode("Still serving", to).Then("Check the logs.")
	return open(open(m, "Logs", SvcPath(label, "logs")), "Open service", SvcPath(label))
}

func ServiceDown(label string, kind spec.Kind, check, text string) notify.Message {
	m := svc(label, fail, "Service down", "Down", envWord(label)+" service is down", text).
		AddCode("Service", label).Add("Check", check).Then("Check the logs.")
	if spec.IsDatabase(kind) {
		return open(m, "Open service", SvcPath(label))
	}
	return open(open(m, "Logs", SvcPath(label, "logs")), "Open service", SvcPath(label))
}

func ServiceUp(label, downFor, text string) notify.Message {
	m := svc(label, ok, "Service up", "Available", envWord(label)+" service is back", text).
		AddCode("Service", label).Add("Down for", downFor)
	return open(m, "Open service", SvcPath(label))
}

func ServiceOOM(label string, kind spec.Kind, limit string, kills int, text string) notify.Message {
	cause := "out of memory"
	if limit != "" {
		cause = "out of its " + limit + " memory limit"
	}
	m := svc(label, fail, "Service restarted", "Failed", envWord(label)+" service ran out of memory", text).
		AddCode("Service", label).Add("Cause", cause).Add("Restarts", plural(kills, "time")).
		Then("It ran out of memory; give it more.")
	if spec.IsDatabase(kind) {
		return open(m, "Open service", SvcPath(label))
	}
	return open(open(m, "Settings", SvcPath(label, "settings")), "Metrics", SvcPath(label, "metrics"))
}

func ServiceOOMOver(label, text string) notify.Message {
	m := svc(label, ok, "Service restarted", "Available", envWord(label)+" service has not run out of memory for an hour", text).
		AddCode("Service", label)
	return open(m, "Open service", SvcPath(label))
}

func ServiceStopped(label, text string) notify.Message {
	m := svc(label, ok, "Service stopped", "Stopped", envWord(label)+" service was stopped", text).AddCode("Service", label)
	return open(m, "Open service", SvcPath(label))
}

func ServerOffline(server, value, runs, text string) notify.Message {
	return srv(server, fail, "Server offline", "Offline", "Server "+server+" is offline", text).
		AddCode("Server", server).Add("Value", value).Add("Runs here", runs).
		Then("Check the server in your provider's console.").Open("Open server", serverPath(server))
}

func ServerOnline(server, offFor, text string) notify.Message {
	return srv(server, ok, "Server online", "Online", "Server "+server+" is back", text).
		AddCode("Server", server).Add("Offline for", offFor).Open("Open server", serverPath(server))
}

func ServerDisk(server string, used, limit int64, text string) notify.Message {
	return srv(server, warn, "Disk space", "High", "Server "+server+" disk is filling up", text).
		AddCode("Server", server).Add("Value", itoa(used)+"% (limit "+itoa(limit)+"%)").
		Then("See what fills it on the server's Disk page.").Open("Disk", serverPath(server, "disk")).Open("Open server", serverPath(server))
}

func ServerDiskOK(server string, used int64, text string) notify.Message {
	return srv(server, ok, "Disk space", "Available", "Server "+server+" disk is under 80% again", text).
		AddCode("Server", server).Add("Value", itoa(used)+"%").Open("Open server", serverPath(server))
}

func ServerMemory(server, top, text string) notify.Message {
	return srv(server, warn, "Memory", "High", "Server "+server+" memory is high", text).
		AddCode("Server", server).Add("Value", "above 90% for 10 minutes (limit 90%)").Add("Top", top).
		Then("Check the top service, or give it a memory limit.").Open("Open server", serverPath(server))
}

func ServerMemoryOK(server, text string) notify.Message {
	return srv(server, ok, "Memory", "Available", "Server "+server+" memory is back under 85%", text).
		AddCode("Server", server).Open("Open server", serverPath(server))
}

func ServerLoad(server, value, busiest, text string) notify.Message {
	return srv(server, warn, "Server load", "High", "Server "+server+" load is high", text).
		AddCode("Server", server).Add("Value", value).Add("Busiest", busiest).
		Then("Check the busiest service or give it a CPU limit.").Open("Open server", serverPath(server))
}

func ServerLoadOK(server, value, text string) notify.Message {
	return srv(server, ok, "Server load", "Available", "Server "+server+" load is back to normal", text).
		AddCode("Server", server).Add("Value", value).Open("Open server", serverPath(server))
}

func ServerForecast(server, value, text string) notify.Message {
	return srv(server, warn, "Disk forecast", "Soon", "Server "+server+" disk will be full within a week", text).
		AddCode("Server", server).Add("Value", value).
		Then("Free space, or give the server a bigger disk.").Open("Disk", serverPath(server, "disk"))
}

func ServerForecastOK(server, text string) notify.Message {
	return srv(server, ok, "Disk forecast", "Available", "Server "+server+" disk is no longer on course to fill within a week", text).
		AddCode("Server", server).Open("Disk", serverPath(server, "disk"))
}

func BackupFailed(label, volume, cause, lastGood, text string) notify.Message {
	m := svc(label, fail, "Backup failed", "Failed", envWord(label)+" backup failed", text).
		AddCode("Service", label).AddCode("Volume", volume).AddCode("Error", firstLine(cause)).Add("Last good", lastGood).
		Then("Check the error, then run a backup by hand.")
	return open(m, "Backups", SvcPath(label, "backups"))
}

func BackupOK(label, volume, text string) notify.Message {
	m := svc(label, ok, "Backup completed", "Completed", envWord(label)+" backup succeeded again", text).
		AddCode("Service", label).AddCode("Volume", volume)
	return open(m, "Backups", SvcPath(label, "backups"))
}

func BackupMissed(label, volume, lastGood string, noStorage bool, text string) notify.Message {
	next := "Check the Backups tab and run one by hand."
	if noStorage {
		next = "Set the backup storage: yawl server storage set."
	}
	m := svc(label, fail, "Backup missed", "Failed", envWord(label)+" backup missed", text).
		AddCode("Service", label).AddCode("Volume", volume).Add("Value", "no backup succeeded in 26 hours (limit 26 hours)").
		Add("Last good", lastGood).Then(next)
	return open(m, "Backups", SvcPath(label, "backups"))
}

func BackupCopyFailed(label, volume, copy, cause, text string) notify.Message {
	m := svc(label, fail, "Backup copy failed", "Failed", "Backup copy to "+copy+" failed", text).
		AddCode("Service", label).AddCode("Volume", volume).Add("Copy", copy).AddCode("Error", firstLine(cause)).
		Then("Check the copy's storage; the backup itself is good.")
	return open(m, "Backups", SvcPath(label, "backups"))
}

func PanelBackupFailed(cause, lastGood, text string) notify.Message {
	return notify.New(fail, "Backup failed", "Failed", "Panel backup failed", text).
		Add("Panel", "this panel").AddCode("Error", firstLine(cause)).Add("Last good", lastGood).
		Then("Check the backup storage.").Open("Backup storage", "/settings/storage")
}

func PanelBackupMissed(lastGood, text string) notify.Message {
	return notify.New(fail, "Backup missed", "Failed", "Panel backup missed", text).
		Add("Panel", "this panel").Add("Value", "no backup succeeded in 26 hours (limit 26 hours)").Add("Last good", lastGood).
		Then("Check the backup storage.").Open("Backup storage", "/settings/storage")
}

func PanelBackupOK(text string) notify.Message {
	return notify.New(ok, "Backup completed", "Completed", "Panel backup succeeded again", text).
		Add("Panel", "this panel").Open("Backup storage", "/settings/storage")
}

func LogAlert(label, what, value, first, text string) notify.Message {
	m := svc(label, warn, "Log alert", "Alert", what, text).
		AddCode("Service", label).Add("Value", value).AddCode("First", first).Then("Check the logs.")
	return open(m, "Logs", SvcPath(label, "logs"))
}

func LogAlertOver(label, line, text string) notify.Message {
	m := svc(label, ok, "Log alert", "Available", line, text).AddCode("Service", label)
	return open(m, "Logs", SvcPath(label, "logs"))
}

func CronFailed(label, cause, text string) notify.Message {
	m := svc(label, fail, "Cron job failed", "Failed", envWord(label)+" cron job failed", text).
		AddCode("Service", label).AddCode("Error", cause).Then("Check the run's output.")
	return open(open(m, "Runs", SvcPath(label, "runs")), "Open service", SvcPath(label))
}

func CronOK(label, text string) notify.Message {
	m := svc(label, ok, "Cron job", "Completed", envWord(label)+" cron job succeeded again", text).AddCode("Service", label)
	return open(m, "Runs", SvcPath(label, "runs"))
}

func NetworkSilent(a, b, value, port, text string) notify.Message {
	return notify.New(fail, "Private network", "Down", a+" cannot reach "+b, text).
		Add("Servers", a+" and "+b).Add("Value", value).Then("Open UDP "+port+" between them.").
		Open("Open "+a, serverPath(a)).Open("Open "+b, serverPath(b))
}

func NetworkOK(a, b, line, text string) notify.Message {
	return notify.New(ok, "Private network", "Online", line, text).Add("Servers", a+" and "+b)
}

func Trashed(label, by, purge, text string) notify.Message {
	return notify.New(warn, "Deleted", "Alert", label+" was deleted", text).
		AddCode("Name", label).Add("By", by).Add("Purged on", purge).
		Then("Restore it from Trash before then if that was a mistake.").Open("Trash", "/activity/trash")
}

func Restored(label, by, text string) notify.Message {
	return notify.New(ok, "Restored", "Available", label+" was restored", text).AddCode("Name", label).Add("By", by)
}

func Purged(label, text string) notify.Message {
	return notify.New(ok, "Purged", "Completed", label+" was purged", text).AddCode("Name", label)
}

func Banned(server, ip, text string) notify.Message {
	return srv(server, warn, "Firewall", "Alert", "An address was banned for SSH", text).
		AddCode("Server", server).AddCode("Address", ip).Open("Firewall", serverPath(server, "firewall"))
}

func WALLag(label, value, text string) notify.Message {
	m := svc(label, warn, "Point in time recovery", "High", "WAL upload is falling behind", text).
		AddCode("Service", label).Add("Value", value).Then("Check the backup storage.")
	return open(m, "Backups", SvcPath(label, "backups"))
}

func MoveFailed(env, to, cause, text string) notify.Message {
	return svc(env, fail, "Move failed", "Failed", "Moving "+env+" to "+to+" failed", text).
		AddCode("Environment", env).AddCode("To", to).AddCode("Error", firstLine(cause)).
		Then("Check the move, then retry it.").Open("Move", "/p/"+env+"/move")
}

func MoveDone(env, to, text string) notify.Message {
	return svc(env, ok, "Move", "Completed", env+" moved to "+to, text).
		AddCode("Environment", env).AddCode("To", to).Open("Open environment", "/p/"+env)
}

func Revealed(label, key, by, text string) notify.Message {
	m := svc(label, warn, "Variable revealed", "Alert", key+" was revealed", text).
		AddCode("Service", label).AddCode("Variable", key).Add("By", by)
	return open(m, "Environment", SvcPath(label, "env"))
}

func WatcherDown(cause, text string) notify.Message {
	return notify.New(fail, "Watcher offline", "Offline", "The watcher is not answering", text).
		AddCode("Error", cause).Then("Alerts come from the panel until it answers; check it in Notifications.").
		Open("Watcher", "/notifications/watcher")
}

func WatcherUp(text string) notify.Message {
	return notify.New(ok, "Watcher online", "Online", "The watcher is answering again", text).Open("Watcher", "/notifications/watcher")
}

func LastGood(ms int64) string {
	if ms == 0 {
		return "never"
	}
	return notify.Date(time.UnixMilli(ms))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func plural(n int, w string) string {
	if n == 1 {
		return "1 " + w
	}
	return strconv.Itoa(n) + " " + w + "s"
}
