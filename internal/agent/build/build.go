package build

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/moby/moby/client"

	"github.com/behzodhalil/yawl/internal/spec"
)

type Request struct {
	Repo, Branch, Commit, Image, Token string
	Secrets                            map[string][]byte
	Keys                               []string
	Env                                string
	Depth                              int
	Builder                            string
	Root                               string
	NoCache                            bool
}

type Built struct{ Commit, Subject string }

func Build(ctx context.Context, c *client.Client, r Request) (Built, []string, error) {
	var warnings []string
	b, err := build(ctx, c, r, &warnings)
	if err != nil && r.Token != "" {
		return Built{}, warnings, errors.New(strings.ReplaceAll(err.Error(), r.Token, "••••"))
	}
	return b, warnings, err
}

func build(ctx context.Context, c *client.Client, r Request, warnings *[]string) (Built, error) {
	dir, head, err := clone(ctx, r)
	if err != nil {
		return Built{}, err
	}
	sha := head.Commit
	defer os.RemoveAll(dir)
	if dir, err = folder(dir, r.Root); err != nil {
		return Built{}, err
	}
	keys := slices.Sorted(maps.Keys(r.Secrets))
	uses := choose(dir, r.Builder)
	*warnings = Warnings(dir, r.Keys, r.Env, uses)
	if uses == "railpack" {
		return head, railpackSolve(ctx, c, dir, r)
	}
	df, err := Generate(dir, keys)
	if err != nil {
		return Built{}, err
	}
	dfDir := dir
	if df != nil {
		if dfDir, err = os.MkdirTemp("", "yawl-dockerfile-"); err != nil {
			return Built{}, err
		}
		defer os.RemoveAll(dfDir)
		if err := os.WriteFile(filepath.Join(dfDir, "Dockerfile"), df, 0o644); err != nil {
			return Built{}, err
		}
	}
	return head, solve(ctx, c, dir, dfDir, r, sha)
}

func folder(dir, root string) (string, error) {
	if root == "" {
		return dir, nil
	}
	base, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(filepath.Join(base, root))
	if err != nil {
		return "", fmt.Errorf("root %s is not in the repository at this commit", root)
	}
	if rel, err := filepath.Rel(base, real); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("root %s leads out of the repository", root)
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("root %s is not a folder", root)
	}
	return real, nil
}

func clone(ctx context.Context, r Request) (dir string, b Built, err error) {
	if dir, err = os.MkdirTemp("", "yawl-build-"); err != nil {
		return "", Built{}, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	repo, err := git.PlainCloneContext(ctx, dir, false, cloneOptions(r))
	if err != nil {
		return "", Built{}, errors.New(redact(r.Repo, cloneCause(r, err)))
	}
	w, err := repo.Worktree()
	if err != nil {
		return "", Built{}, err
	}
	if r.Commit != "" {
		if err := w.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(r.Commit)}); err != nil {
			return "", Built{}, fmt.Errorf("checkout %s: %w", r.Commit, err)
		}
	}
	head, err := repo.Head()
	if err != nil {
		return "", Built{}, err
	}
	b = Built{Commit: head.Hash().String()}
	if c, err := repo.CommitObject(head.Hash()); err == nil {
		b.Subject = subject(c.Message)
	}
	return dir, b, nil
}

func subject(msg string) string {
	for _, l := range strings.Split(msg, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

func cloneCause(r Request, err error) string {
	if r.Token != "" || !errors.Is(err, transport.ErrAuthenticationRequired) {
		return fmt.Sprintf("clone %s: %v", r.Repo, err)
	}
	if _, ok := spec.GitHubRepo(r.Repo); ok {
		return fmt.Sprintf("clone %s: the repository is private or does not exist. Connect GitHub: yawl github connect", r.Repo)
	}
	return fmt.Sprintf("clone %s: the repository is private or does not exist. Connect its GitLab or Gitea under Integrations, with a token.", r.Repo)
}

func cloneOptions(r Request) *git.CloneOptions {
	o := &git.CloneOptions{URL: r.Repo, Depth: r.Depth}
	if name, ok := spec.GitHubRepo(r.Repo); ok && strings.HasPrefix(r.Repo, "git@") {
		o.URL = "https://github.com/" + name + ".git" // an SSH URL cannot carry the installation token, and the agent has no SSH key
	}
	if r.Branch != "" {
		o.ReferenceName = plumbing.NewBranchReferenceName(r.Branch)
	}
	if r.Token != "" {
		user := "x-access-token"
		if _, ok := spec.GitHubRepo(r.Repo); !ok {
			user = "oauth2" // a GitLab access token (design record 34)
		}
		o.Auth = &http.BasicAuth{Username: user, Password: r.Token}
	}
	return o
}

func redact(repo, msg string) string {
	u, err := url.Parse(repo)
	if err != nil || u.User == nil {
		return msg
	}
	if pw, ok := u.User.Password(); ok && pw != "" {
		msg = strings.ReplaceAll(msg, pw, "xxxxx")
	} else if name := u.User.Username(); name != "" {
		msg = strings.ReplaceAll(msg, name, "xxxxx") // https://<token>@host puts the token in the user name
	}
	return msg
}
