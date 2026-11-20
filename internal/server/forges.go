package server

import (
	"context"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/api"
	"github.com/behzodhalil/yawl/internal/server/gateway"
	"github.com/behzodhalil/yawl/internal/server/gitea"
	"github.com/behzodhalil/yawl/internal/server/github"
	"github.com/behzodhalil/yawl/internal/server/gitlab"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

type forgeTokens struct {
	gh github.Tokens
	gl gitlab.Tokens
	gt gitea.Tokens
}

func (f forgeTokens) Token(ctx context.Context, repo string) (string, error) {
	if _, ok := spec.GitHubRepo(repo); ok {
		return f.gh.Token(ctx, repo)
	}
	if key, ok := spec.RepoKey(repo); ok && isGitea(ctx, f.gt.Q, key) {
		return f.gt.Token(ctx, repo)
	}
	return f.gl.Token(ctx, repo)
}

func isGitea(ctx context.Context, q *db.Queries, key string) bool {
	host, _, _ := strings.Cut(key, "/")
	return q != nil && gitea.Has(ctx, q, host)
}

type forgeComments struct {
	gh github.Comments
	gl gitlab.Comments
	gt gitea.Comments
}

func (f forgeComments) Comment(ctx context.Context, key string, number, id int64, body string) (int64, error) {
	if isGitea(ctx, f.gt.Q, key) {
		return f.gt.Comment(ctx, key, number, id, body)
	}
	if host, _, _ := strings.Cut(key, "/"); strings.Contains(host, ".") {
		return f.gl.Comment(ctx, key, number, id, body)
	}
	return f.gh.Comment(ctx, key, number, id, body)
}

type forgeStatuses struct {
	gh github.Statuses
	gl gitlab.Statuses
	gt gitea.Statuses
}

func (f forgeStatuses) Status(ctx context.Context, key, sha, state, name, description, target string) error {
	if isGitea(ctx, f.gt.Q, key) {
		return f.gt.Status(ctx, key, sha, state, name, description, target)
	}
	if host, _, _ := strings.Cut(key, "/"); strings.Contains(host, ".") {
		return f.gl.Status(ctx, key, sha, state, name, description, target)
	}
	return f.gh.Status(ctx, key, sha, state, name, description, target)
}

type gatewayTerms struct{ gw *gateway.Gateway }

func (g gatewayTerms) OpenTerm(ctx context.Context, server, container string, cols, rows int) (api.TermSession, error) {
	return g.gw.OpenTerm(ctx, server, container, cols, rows)
}
