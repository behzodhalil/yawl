package app

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

type PR struct {
	Action, Title, Branch, Sha string
	State                      string
	Number                     int64
	Repo, HeadRepo             string
	Sender                     string
	Base                       string
	Merged                     bool
	MergeSha                   string
}

func PreviewSendersKey(projectID string) string { return "previews.senders." + projectID }

func senderOK(ctx context.Context, q *db.Queries, projectID, sender string) (bool, error) {
	list, err := q.GetSetting(ctx, PreviewSendersKey(projectID))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && list == "") {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return sender != "" && slices.Contains(strings.Split(list, "\n"), strings.ToLower(sender)), nil
}

func (a *App) PullRequest(ctx context.Context, pr PR) error {
	repo := strings.ToLower(pr.Repo)
	now := a.Now().UnixMilli()
	if pr.Action == "closed" || pr.State == "closed" {
		if err := a.St.Q.RetitlePreviews(ctx, db.RetitlePreviewsParams{Title: pr.Title, UpdatedAt: now, Repo: repo, Number: pr.Number}); err != nil {
			return err
		}
		if err := a.St.Q.ClosedPreviewOfPR(ctx, db.ClosedPreviewOfPRParams{UpdatedAt: now, Repo: repo, Number: pr.Number}); err != nil {
			return err
		}
		if pr.Merged && pr.MergeSha != "" {
			if err := a.St.Q.SetPreviewsMerged(ctx, db.SetPreviewsMergedParams{Base: pr.Base, MergeSha: pr.MergeSha, MergedAt: now, UpdatedAt: now, Repo: repo, Number: pr.Number}); err != nil {
				return err
			}
		}
		return a.St.Q.ClosePreviewsOfPR(ctx, db.ClosePreviewsOfPRParams{UpdatedAt: now, Repo: repo, Number: pr.Number})
	}
	if pr.Action == "edited" {
		return a.St.Q.RetitlePreviews(ctx, db.RetitlePreviewsParams{Title: pr.Title, UpdatedAt: now, Repo: repo, Number: pr.Number})
	}
	srcs, err := a.St.Q.PreviewSources(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	return a.St.Tx(ctx, func(q *db.Queries) error {
		for _, s := range srcs {
			if name, ok := spec.RepoKey(s.Repo); !ok || name != repo || seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			switch pr.Action {
			case "opened", "reopened", "synchronize":
				state := "waiting"
				if !strings.EqualFold(pr.HeadRepo, pr.Repo) {
					state = "fork"
				} else if ok, err := senderOK(ctx, q, s.ProjectID, pr.Sender); err != nil {
					return err
				} else if !ok {
					state = "held"
				}
				if err := q.UpsertPreview(ctx, db.UpsertPreviewParams{ID: store.NewID("pre_"), ProjectID: s.ProjectID, SourceEnvID: s.ID,
					Number: pr.Number, Repo: repo, Title: pr.Title, Branch: pr.Branch, HeadSha: pr.Sha, State: state, Author: pr.Sender, Base: pr.Base,
					LastPushAt: now, CreatedAt: now, UpdatedAt: now}); err != nil {
					return err
				}
				if state == "held" {
					if p, err := q.PreviewOfPR(ctx, db.PreviewOfPRParams{ProjectID: s.ProjectID, Repo: repo, Number: pr.Number}); err != nil {
						return err
					} else if p.EnvID != "" {
						if err := RemovePreviewEnv(ctx, q, p.EnvID, now); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	})
}

func (a *App) RenewPreviews(ctx context.Context, fullName, branch, sha, sender string) error {
	now := a.Now().UnixMilli()
	return a.St.Tx(ctx, func(q *db.Queries) error {
		rows, err := q.PreviewsOnBranch(ctx, db.PreviewsOnBranchParams{Repo: strings.ToLower(fullName), Branch: branch})
		if err != nil {
			return err
		}
		for _, p := range rows {
			ok, err := senderOK(ctx, q, p.ProjectID, sender)
			if err != nil {
				return err
			}
			if ok {
				err = q.RenewPreview(ctx, db.RenewPreviewParams{Now: now, Sha: sha, ID: p.ID})
			} else if err = q.HoldPreview(ctx, db.HoldPreviewParams{UpdatedAt: now, ID: p.ID}); err == nil && p.EnvID != "" {
				err = RemovePreviewEnv(ctx, q, p.EnvID, now)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func RemovePreviewEnv(ctx context.Context, q *db.Queries, envID string, now int64) error {
	svcs, err := q.ListServicesInEnv(ctx, envID)
	if err != nil {
		return err
	}
	for _, s := range svcs {
		if err := q.SupersedeQueued(ctx, db.SupersedeQueuedParams{UpdatedAt: now, ServiceID: s.ID}); err != nil {
			return err
		}
	}
	if err := q.MarkPreviewServicesGone(ctx, db.MarkPreviewServicesGoneParams{Now: sql.NullInt64{Int64: now, Valid: true}, EnvID: envID}); err != nil {
		return err
	}
	return q.MarkPreviewEnvGone(ctx, db.MarkPreviewEnvGoneParams{Now: sql.NullInt64{Int64: now, Valid: true}, ID: envID})
}
