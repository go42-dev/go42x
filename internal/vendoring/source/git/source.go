// Package git resolves repository refs and fetches locked commits.
package git

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	getter "github.com/hashicorp/go-getter/v2"

	"github.com/go42-dev/go42x/internal/vendoring/source"
)

type Adapter struct{}

func New() *Adapter {
	return &Adapter{}
}

func (a *Adapter) Fetch(
	ctx context.Context,
	s source.Config,
	locked *source.Resolved,
	workspace string,
) (source.Result, error) {
	ref := s.Ref
	if locked != nil {
		ref = locked.Commit
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return source.Result{}, err
	}

	query := u.Query()
	query.Set("ref", ref)
	query.Set("archive", "false")
	u.RawQuery = query.Encode()
	root := filepath.Join(workspace, "repository")
	client := getter.Client{
		DisableSymlinks: true,
		Getters:         []getter.Getter{&getter.GitGetter{}},
	}
	if _, err := client.Get(
		ctx,
		&getter.Request{
			Src:     "git::" + u.String(),
			Dst:     root,
			GetMode: getter.ModeDir,
		},
	); err != nil {
		return source.Result{}, err
	}

	if err := source.ValidateTree(ctx, root); err != nil {
		return source.Result{}, err
	}

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return source.Result{}, fmt.Errorf("read resolved Git commit: %w", err)
	}

	commit := strings.TrimSpace(string(output))
	if locked != nil && commit != locked.Commit {
		return source.Result{}, fmt.Errorf("fetched Git commit differs from lock")
	}

	return source.Result{
		Root:     root,
		Resolved: source.Resolved{Commit: commit},
	}, nil
}
