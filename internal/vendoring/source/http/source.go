// Package http fetches files and archives with content identities.
package http

import (
	"context"
	"net/http"

	"github.com/go42-dev/go42x/internal/vendoring/source"
)

type Adapter struct{ client *http.Client }

func New(client *http.Client) *Adapter {
	return &Adapter{client: client}
}

func (a *Adapter) Fetch(
	ctx context.Context,
	s source.Config,
	locked *source.Resolved,
	workspace string,
) (source.Result, error) {
	expected := s.SHA256
	if locked != nil {
		expected = locked.SHA256
	}
	checksum := ""
	if len(expected) > 0 {
		checksum = "sha256:" + expected
	}
	root, hash, err := source.FetchURL(ctx, a.client, s.URL, checksum, expected, workspace, "")
	if err != nil {
		return source.Result{}, err
	}

	return source.Result{
		Root:     root,
		Resolved: source.Resolved{SHA256: hash},
	}, nil
}
