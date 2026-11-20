package workers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/deploy"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

type Previews struct {
	St       *store.Store
	App      *app.App
	GH       Commenter
	Now      func() time.Time
	PanelURL string
}

type Commenter interface {
	Comment(ctx context.Context, repo string, number, id int64, body string) (int64, error)
}

const (
	maxPreviews = 5
	previewIdle = 7 * 24 * time.Hour
	mergeWatch  = 24 * time.Hour
)

func (w *Previews) Tick(ctx context.Context) error {
	if app.Adopting(ctx, w.St.Q) {
		return nil
	}
	rows, err := w.St.Q.OpenPreviews(ctx)
	if err != nil {
		return err
	}
	now := w.Now()
	running := map[string]int{}
	for _, p := range rows {
		if p.State == "making" || p.State == "live" {
			running[p.ProjectID]++
		}
	}
	removed := map[string]bool{}
	for _, p := range rows {
		idle := (p.State == "making" || p.State == "live" || p.State == "failed") && now.Sub(time.UnixMilli(p.LastPushAt)) > previewIdle
		if p.State != "closing" && !idle {
			continue
		}
		if err := w.remove(ctx, p); err != nil {
			return err
		}
		removed[p.ID] = true
		if p.State == "making" || p.State == "live" {
			running[p.ProjectID]--
		}
	}
	for _, p := range rows {
		if removed[p.ID] {
			continue
		}
		switch {
		case p.State == "waiting" && running[p.ProjectID] < maxPreviews:
			if err := w.make(ctx, p); err != nil {
				return err
			}
			running[p.ProjectID]++
		case p.State == "making":
			if err := w.queueApps(ctx, p); err != nil {
				return err
			}
		}
	}
	return w.comments(ctx)
}

func (w *Previews) comments(ctx context.Context) error {
	if w.GH == nil {
		return nil
	}
	rows, err := w.St.Q.CommentablePreviews(ctx)
	if err != nil {
		return err
	}
	for _, p := range rows {
		text, done, err := w.comment(ctx, p)
		if err != nil {
			return err
		}
		if text == "" || text == p.Comment {
			if done {
				if err := w.St.Q.SetPreviewCommentDone(ctx, p.ID); err != nil {
					return err
				}
			}
			continue
		}
		k, now := "preview.comment_failed."+p.ID, w.Now()
		if at, failed, ok := strings.Cut(setting(ctx, w.St.Q, k), " "); ok && failed == text {
			if ms, err := strconv.ParseInt(at, 10, 64); err == nil && now.Sub(time.UnixMilli(ms)) < time.Hour {
				continue
			}
		}
		id, err := w.GH.Comment(ctx, p.Repo, p.Number, p.CommentID, text)
		if err != nil {
			slog.Warn("preview comment", "repo", p.Repo, "pr", p.Number, "err", err)
			w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: strconv.FormatInt(now.UnixMilli(), 10) + " " + text})
			continue
		}
		w.St.Q.DeleteSettingKey(ctx, k)
		if err := w.St.Q.SetPreviewComment(ctx, db.SetPreviewCommentParams{CommentID: id, Comment: text, UpdatedAt: now.UnixMilli(), ID: p.ID}); err != nil {
			return err
		}
		if done {
			if err := w.St.Q.SetPreviewCommentDone(ctx, p.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *Previews) comment(ctx context.Context, p db.Preview) (string, bool, error) {
	q := w.St.Q
	src, err := q.EnvProject(ctx, p.SourceEnvID)
	if err != nil {
		return "", false, err
	}
	var rows [][2]string
	done := false
	if p.State == "closed" {
		if p.EnvID == "" {
			return "", true, nil
		}
		done = true
		if p.MergeSha != "" {
			merged, final, err := mergeRows(ctx, q, p)
			if err != nil {
				return "", false, err
			}
			rows = merged
			done = final || w.Now().Sub(time.UnixMilli(p.MergedAt)) > mergeWatch
		}
		rows = append(rows, [2]string{"Preview", "🗑 Removed"})
	} else {
		line, err := PreviewText(ctx, q, p)
		if err != nil || line == "" {
			return "", false, err
		}
		d, err := previewDeploy(ctx, q, p)
		if err != nil {
			return "", false, err
		}
		switch {
		case p.State == "live" && d != nil && d.state == "live":
			rows = append(rows, [2]string{"Preview", "✅ " + d.link()}, [2]string{"Commit", "`" + short(d.sha) + "`"})
		case p.State == "live" && d != nil:
			rows = append(rows, [2]string{"Preview", fmt.Sprintf("❌ Deploy of `%s` failed: %s", short(d.sha), d.cause)})
		default:
			icon := map[string]string{"waiting": "⏳", "fork": "🚫", "held": "⏸️", "failed": "❌"}[p.State]
			rows = append(rows, [2]string{"Preview", strings.TrimSpace(icon + " " + strings.TrimPrefix(line, "Preview: "))})
		}
		if d != nil && w.PanelURL != "" {
			rows = append(rows, [2]string{"Logs", fmt.Sprintf("[%s / pr-%d / %s](%s/p/%s/pr-%d/s/%s)", src.Project, p.Number, d.svc,
				strings.TrimSuffix(w.PanelURL, "/"), src.Project, p.Number, d.svc)})
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Yawl** · %s / pr-%d\n\n| | |\n|---|---|\n", src.Project, p.Number)
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s |\n", r[0], cell(r[1]))
	}
	return strings.TrimSuffix(b.String(), "\n"), done, nil
}

// cell keeps a value on its table row: a pipe or a line break would end it.
func cell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", "").Replace(s)
}

func mergeRows(ctx context.Context, q *db.Queries, p db.Preview) ([][2]string, bool, error) {
	targets, err := q.MergeTargets(ctx, db.MergeTargetsParams{ProjectID: p.ProjectID, Branch: p.Base})
	if err != nil {
		return nil, false, err
	}
	var rows [][2]string
	final := true
	for _, t := range targets {
		if name, ok := spec.RepoKey(t.Repo); !ok || name != p.Repo {
			continue
		}
		label := t.Env + " · " + t.Name
		d, err := q.DeployOfCommit(ctx, db.DeployOfCommitParams{ServiceID: t.ID, CommitSha: p.MergeSha})
		switch {
		case errors.Is(err, sql.ErrNoRows):
			n, err := q.PushSkipped(ctx, db.PushSkippedParams{ServiceID: t.ID, Repo: p.Repo, Commit: p.MergeSha})
			if err != nil {
				return nil, false, err
			}
			if n > 0 {
				rows = append(rows, [2]string{label, "➖ Not deployed: this change does not touch it"})
			} else {
				rows = append(rows, [2]string{label, fmt.Sprintf("⏳ Waiting for the push to `%s`", p.Base)})
				final = false
			}
		case err != nil:
			return nil, false, err
		case d.State == "live":
			v := "✅ Live"
			if t.Kind == string(spec.KindWeb) {
				u, err := webURL(ctx, q, t.ID, t.Name, t.Env, t.PublicIp)
				if err != nil {
					return nil, false, err
				}
				v = "✅ " + mdLink(u)
			}
			rows = append(rows, [2]string{label, fmt.Sprintf("%s · `%s`", v, short(d.CommitSha))})
		case d.State == "failed" || d.State == "rolled_back":
			rows = append(rows, [2]string{label, fmt.Sprintf("❌ Deploy of `%s` failed: %s", short(d.CommitSha), d.Cause)})
		case d.State == "superseded":
			rows = append(rows, [2]string{label, "➖ Superseded by a newer deploy"})
		default:
			rows = append(rows, [2]string{label, fmt.Sprintf("⏳ Deploying `%s`", short(d.CommitSha))})
			final = false
		}
	}
	if len(rows) == 0 {
		rows = append(rows, [2]string{"Merged", fmt.Sprintf("➖ Nothing here deploys `%s`", p.Base)})
	}
	return rows, final, nil
}

func webURL(ctx context.Context, q *db.Queries, serviceID, name, env, publicIP string) (string, error) {
	ds, err := q.ListDomains(ctx, serviceID)
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.DnsState == "ok" {
			return "https://" + d.Host, nil
		}
	}
	ip, err := netip.ParseAddr(publicIP)
	if err != nil {
		return "", nil
	}
	return "https://" + app.FreeHost(name, env, ip), nil
}

func mdLink(u string) string { return fmt.Sprintf("[%s](%s)", strings.TrimPrefix(u, "https://"), u) }

type shownDeploy struct{ svc, kind, url, sha, state, cause string }

func (d *shownDeploy) link() string {
	if d.url == "" {
		return "Live"
	}
	return mdLink(d.url)
}

func previewDeploy(ctx context.Context, q *db.Queries, p db.Preview) (*shownDeploy, error) {
	if p.EnvID == "" {
		return nil, nil
	}
	svcs, err := q.ListServicesInEnv(ctx, p.EnvID)
	if err != nil {
		return nil, err
	}
	var pick *db.Service
	for i, s := range svcs {
		if name, ok := spec.RepoKey(s.Repo); ok && name == p.Repo && (pick == nil || (s.Kind == string(spec.KindWeb) && pick.Kind != string(spec.KindWeb))) {
			pick = &svcs[i]
		}
	}
	if pick == nil {
		return nil, nil
	}
	last, err := q.ListDeploysForService(ctx, db.ListDeploysForServiceParams{ServiceID: pick.ID, Limit: 1})
	if err != nil || len(last) == 0 {
		return nil, err
	}
	d := &shownDeploy{svc: pick.Name, kind: pick.Kind, sha: last[0].CommitSha, state: last[0].State, cause: last[0].Cause}
	if d.state == "rolled_back" {
		d.state = "failed"
	}
	if d.state != "live" && d.state != "failed" {
		return nil, nil
	}
	if d.state == "live" && pick.Kind == string(spec.KindWeb) {
		e, err := q.EnvProject(ctx, p.EnvID)
		if err != nil {
			return nil, err
		}
		srv, err := q.GetServer(ctx, e.ServerID)
		if err != nil {
			return nil, err
		}
		if d.url, err = webURL(ctx, q, pick.ID, pick.Name, e.Name, srv.PublicIp); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func PreviewText(ctx context.Context, q *db.Queries, p db.Preview) (string, error) {
	switch p.State {
	case "failed":
		return "Preview: " + p.Cause, nil
	case "fork":
		return "Previews are not made for changes from forks: their code would run on this server.", nil
	case "held":
		return "Preview held: the last change came from someone not on this project's preview list. A push from someone on it makes the preview.", nil
	case "closed":
		if p.EnvID == "" {
			return "", nil
		}
		return "Preview removed.", nil
	case "waiting":
		n, err := q.RunningPreviews(ctx, p.ProjectID)
		if err != nil || n < maxPreviews {
			return "", err
		}
		return fmt.Sprintf("pr-%d waits: %d previews are running. It starts when one closes.", p.Number, maxPreviews), nil
	}
	d, err := previewDeploy(ctx, q, p)
	if err != nil || d == nil {
		return "", err
	}
	if d.state == "failed" {
		return fmt.Sprintf("Preview: deploy of %s failed: %s", short(d.sha), d.cause), nil
	}
	url := ""
	if d.url != "" {
		url = d.url + " "
	}
	return fmt.Sprintf("Preview: %s(deployed %s)", url, short(d.sha)), nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (w *Previews) make(ctx context.Context, p db.Preview) error {
	if ok, err := w.St.Q.PreviewSourceOK(ctx, p.SourceEnvID); err != nil {
		return err
	} else if ok == 0 {
		return w.St.Q.SetPreviewState(ctx, db.SetPreviewStateParams{State: "closed", EnvID: "", UpdatedAt: w.Now().UnixMilli(), ID: p.ID})
	}
	src, err := w.St.Q.EnvProject(ctx, p.SourceEnvID)
	if err != nil {
		return err
	}
	if _, err := w.St.Q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: src.Project, Env: fmt.Sprintf("pr-%d", p.Number)}); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	all, err := w.St.Q.ListServicesInEnv(ctx, p.SourceEnvID)
	if err != nil {
		return err
	}
	var svcs []db.Service
	left := map[string]bool{}
	for _, s := range all {
		if s.NoPreview == 1 {
			left[s.Name] = true
			continue
		}
		svcs = append(svcs, s)
	}
	vars, err := w.St.Q.EnvVarsAll(ctx, p.SourceEnvID)
	if err != nil {
		return err
	}
	for _, v := range vars {
		if to, _, ok := strings.Cut(v.Ref, "."); ok && left[to] && (v.ServiceID == "" || !isLeft(all, v.ServiceID)) {
			cause := fmt.Sprintf("not made: %s refers to %s, which has \"preview: false\".", v.Key, to)
			return w.St.Q.SetPreviewFailure(ctx, db.SetPreviewFailureParams{Cause: cause, UpdatedAt: w.Now().UnixMilli(), ID: p.ID})
		}
	}
	for _, v := range vars {
		if strings.Contains(v.Ref, "/") && !(v.ServiceID != "" && isLeft(all, v.ServiceID)) {
			cause := fmt.Sprintf("not made: %s refers to %s, another environment's database, which this pull request's migrations would change.", v.Key, v.Ref)
			return w.St.Q.SetPreviewFailure(ctx, db.SetPreviewFailureParams{Cause: cause, UpdatedAt: w.Now().UnixMilli(), ID: p.ID})
		}
	}
	now := w.Now().UnixMilli()
	var dbs []string
	envID := ""
	err = w.St.Tx(ctx, func(q *db.Queries) error {
		before, err := q.CountLiveServices(ctx)
		if err != nil {
			return err
		}
		if envID, err = q.UpsertEnvironment(ctx, db.UpsertEnvironmentParams{ID: store.NewID("env_"), ProjectID: src.ProjectID,
			Name: fmt.Sprintf("pr-%d", p.Number), ServerID: src.ServerID, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := q.SetPreviewOf(ctx, db.SetPreviewOfParams{PreviewOf: p.SourceEnvID, UpdatedAt: now, ID: envID}); err != nil {
			return err
		}
		ids := map[string]string{}
		for _, s := range svcs {
			if s.Kind == string(spec.KindCron) {
				continue
			}
			if s.Kind == string(spec.KindWeb) {
				if err := app.CheckFreeHost(ctx, q, src.ServerID, src.Project, fmt.Sprintf("pr-%d", p.Number), s.Name); err != nil {
					return err
				}
			}
			branch := s.Branch
			if name, ok := spec.RepoKey(s.Repo); ok && name == p.Repo {
				branch = p.Branch
			}
			id, err := q.UpsertService(ctx, db.UpsertServiceParams{ID: store.NewID("svc_"), EnvironmentID: envID, Name: s.Name, Kind: s.Kind,
				Repo: s.Repo, Branch: branch, Port: s.Port, HealthPath: s.HealthPath, HealthTimeoutMs: s.HealthTimeoutMs, DrainTimeoutMs: s.DrainTimeoutMs,
				AutoDeploy: 0, Version: s.Version, BackupAt: s.BackupAt, ReleaseJson: s.ReleaseJson, ReleaseTimeoutMs: s.ReleaseTimeoutMs,
				AllowJson: "[]", Schedule: s.Schedule, Timezone: s.Timezone, CommandJson: s.CommandJson, TimeoutMs: s.TimeoutMs,
				VolumesJson: s.VolumesJson, Cpu: s.Cpu, Memory: s.Memory, Image: s.Image, CreatedAt: now, UpdatedAt: now,
				Builder: s.Builder, Root: s.Root, Replicas: max(s.Replicas, 1), TagsJson: s.TagsJson, WatchJson: s.WatchJson, Www: s.Www, Noindex: s.Noindex, KeepJson: s.KeepJson})
			if err != nil {
				return err
			}
			ids[s.ID] = id
			if s.Kind == string(spec.KindWeb) && src.PreviewDomain != "" {
				host := fmt.Sprintf("%s-pr-%d.%s", s.Name, p.Number, src.PreviewDomain)
				if _, err := q.DomainOwner(ctx, host); errors.Is(err, sql.ErrNoRows) {
					if err := q.InsertDomain(ctx, db.InsertDomainParams{ID: store.NewID("dom_"), ServiceID: id, Host: host, CreatedAt: now, UpdatedAt: now}); err != nil {
						return err
					}
					if err := app.UnqueueRemoval(ctx, q, src.ServerID, host); err != nil {
						return err
					}
				} else if err != nil {
					return err
				}
			}
			if spec.IsDatabase(spec.Kind(s.Kind)) {
				dbs = append(dbs, id)
			}
		}
		for _, v := range vars {
			to, ok := ids[v.ServiceID]
			if v.ServiceID != "" && !ok {
				continue
			}
			if v.ServiceID != "" && isDB(svcs, v.ServiceID) {
				continue
			}
			if err := q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{ID: store.NewID("var_"), EnvironmentID: envID, ServiceID: to, Key: v.Key,
				AgentBox: v.AgentBox, RecoveryBox: v.RecoveryBox, Build: v.Build, Ref: v.Ref, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		}
		srcs, err := q.SourcesForEnv(ctx, p.SourceEnvID)
		if err != nil {
			return err
		}
		for _, x := range srcs {
			if err := q.UpsertSource(ctx, db.UpsertSourceParams{ID: store.NewID("src_"), EnvironmentID: envID, Provider: x.Provider, Url: x.Url, Path: x.Path,
				Workspace: x.Workspace, Env: x.Env, AgentBox: x.AgentBox, RecoveryBox: x.RecoveryBox, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		}
		if err := w.App.CheckServices(ctx, q, before); err != nil {
			return err
		}
		return q.SetPreviewMade(ctx, db.SetPreviewMadeParams{EnvID: envID, LastPushAt: now, UpdatedAt: now, ID: p.ID})
	})
	if errors.As(err, new(app.LimitError)) || errors.As(err, new(app.HostTaken)) {
		return w.St.Q.SetPreviewFailure(ctx, db.SetPreviewFailureParams{Cause: "not made: " + err.Error(), UpdatedAt: w.Now().UnixMilli(), ID: p.ID})
	}
	if err != nil {
		return err
	}
	for _, id := range dbs {
		if _, err := w.App.Deploy(ctx, id, "", "preview"); err != nil {
			return err
		}
	}
	p.EnvID = envID
	return w.queueApps(ctx, p)
}

func isLeft(svcs []db.Service, id string) bool {
	for _, s := range svcs {
		if s.ID == id {
			return s.NoPreview == 1
		}
	}
	return false
}

func isDB(svcs []db.Service, id string) bool {
	for _, s := range svcs {
		if s.ID == id {
			return spec.IsDatabase(spec.Kind(s.Kind))
		}
	}
	return false
}

func (w *Previews) queueApps(ctx context.Context, p db.Preview) error {
	if p.EnvID == "" {
		return w.make(ctx, p)
	}
	svcs, err := w.St.Q.ListServicesInEnv(ctx, p.EnvID)
	if err != nil {
		return err
	}
	var apps []db.Service
	for _, s := range svcs {
		if !spec.IsDatabase(spec.Kind(s.Kind)) {
			apps = append(apps, s)
			continue
		}
		if s.CurrentDeployID.Valid {
			continue
		}
		last, err := w.St.Q.ListDeploysForService(ctx, db.ListDeploysForServiceParams{ServiceID: s.ID, Limit: 1})
		if err != nil {
			return err
		}
		if len(last) == 1 && (last[0].State == "failed" || last[0].State == "rolled_back") {
			if last[0].CreatedAt < p.LastPushAt {
				_, err := w.App.Deploy(ctx, s.ID, "", "preview")
				return err
			}
			return w.St.Q.SetPreviewFailure(ctx, db.SetPreviewFailureParams{Cause: fmt.Sprintf("%s failed: %s", s.Name, last[0].Cause), UpdatedAt: w.Now().UnixMilli(), ID: p.ID})
		}
		return nil
	}
	src, err := w.St.Q.ListServicesInEnv(ctx, p.SourceEnvID)
	if err != nil {
		return err
	}
	for _, s := range apps {
		commit := ""
		if name, ok := spec.RepoKey(s.Repo); ok && name == p.Repo {
			commit = p.HeadSha
		}
		if _, err := w.App.Deploy(ctx, s.ID, commit, "preview"); err != nil && !strings.Contains(err.Error(), "deploy in progress") {
			return err
		}
		auto := int64(1)
		if name, ok := spec.RepoKey(s.Repo); !ok || name != p.Repo {
			auto = 0
			for _, o := range src {
				if o.Name == s.Name {
					auto = o.AutoDeploy
				}
			}
		}
		if err := w.St.Q.SetAutoDeploy(ctx, db.SetAutoDeployParams{AutoDeploy: auto, UpdatedAt: w.Now().UnixMilli(), ID: s.ID}); err != nil {
			return err
		}
	}
	return w.St.Q.SetPreviewState(ctx, db.SetPreviewStateParams{State: "live", EnvID: p.EnvID, UpdatedAt: w.Now().UnixMilli(), ID: p.ID})
}

func (w *Previews) remove(ctx context.Context, p db.Preview) error {
	now := w.Now().UnixMilli()
	return w.St.Tx(ctx, func(q *db.Queries) error {
		if p.EnvID != "" {
			if err := app.RemovePreviewEnv(ctx, q, p.EnvID, now); err != nil {
				return err
			}
		}
		return q.SetPreviewState(ctx, db.SetPreviewStateParams{State: "closed", EnvID: p.EnvID, UpdatedAt: now, ID: p.ID})
	})
}

type Card struct {
	Service  string      `json:"service,omitempty"`
	DeployID string      `json:"deploy_id,omitempty"`
	URL      string      `json:"url,omitempty"`
	Commit   string      `json:"commit,omitempty"`
	Cause    string      `json:"cause,omitempty"`
	Steps    []CardStep  `json:"steps,omitempty"`
	Landed   [][2]string `json:"landed,omitempty"`
}

type CardStep struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Ms    int64  `json:"ms,omitempty"`
}

func PreviewCard(ctx context.Context, q *db.Queries, p db.Preview) (Card, error) {
	var c Card
	if p.State == "closed" {
		if p.MergeSha != "" {
			rows, _, err := mergeRows(ctx, q, p)
			c.Landed = append(rows, [2]string{"Preview", "Removed"})
			return c, err
		}
		return c, nil
	}
	if p.State == "failed" {
		c.Cause = p.Cause
	}
	if p.EnvID == "" {
		return c, nil
	}
	svcs, err := q.ListServicesInEnv(ctx, p.EnvID)
	if err != nil {
		return c, err
	}
	var pick *db.Service
	for i, s := range svcs {
		if name, ok := spec.RepoKey(s.Repo); ok && name == p.Repo && (pick == nil || (s.Kind == string(spec.KindWeb) && pick.Kind != string(spec.KindWeb))) {
			pick = &svcs[i]
		}
	}
	if pick == nil {
		return c, nil
	}
	c.Service = pick.Name
	last, err := q.ListDeploysForService(ctx, db.ListDeploysForServiceParams{ServiceID: pick.ID, Limit: 1})
	if err != nil || len(last) == 0 {
		return c, err
	}
	d := last[0]
	c.DeployID, c.Commit = d.ID, d.CommitSha
	steps, err := q.DeploySteps(ctx, d.ID)
	if err != nil {
		return c, err
	}
	build, run := CardStep{Name: "build", State: "todo"}, CardStep{Name: "deploy", State: "todo"}
	live := CardStep{Name: "live", State: "todo"}
	var deployMs int64
	for _, st := range steps {
		ms := int64(0)
		if st.FinishedAt.Valid {
			ms = st.FinishedAt.Int64 - st.StartedAt
		}
		if st.Step == string(deploy.StepBuild) || st.Step == string(deploy.StepPull) {
			build.State, build.Ms = map[string]string{"ok": "done", "running": "run", "failed": "fail"}[st.State], ms
			continue
		}
		deployMs += ms
		switch st.State {
		case "running":
			run.State = "run"
		case "failed":
			run.State = "fail"
		}
	}
	switch d.State {
	case "queued":
		build.State = "run"
	case "running":
		if build.State == "done" && run.State == "todo" {
			run.State = "run"
		}
	case "live":
		build.State, run.State, live.State = "done", "done", "done"
		run.Ms = deployMs
		if pick.Kind == string(spec.KindWeb) {
			e, err := q.EnvProject(ctx, p.EnvID)
			if err != nil {
				return c, err
			}
			srv, err := q.GetServer(ctx, e.ServerID)
			if err != nil {
				return c, err
			}
			if c.URL, err = webURL(ctx, q, pick.ID, pick.Name, e.Name, srv.PublicIp); err != nil {
				return c, err
			}
		}
	case "failed", "rolled_back":
		c.Cause = d.Cause
		if build.State != "fail" {
			build.State, run.State = "done", "fail"
		}
	}
	if build.State == "" {
		build.State = "todo"
	}
	c.Steps = []CardStep{build, run, live}
	return c, nil
}
