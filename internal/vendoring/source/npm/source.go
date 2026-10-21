// Package npm resolves registry metadata without running npm or package scripts.
package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/go42-dev/go42x/internal/vendoring/source"
)

const (
	headerAccept      = "Accept"
	maxMetadataBytes  = 16 << 20
	metadataMediaType = "application/vnd.npm.install-v1+json"
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
	var resolved source.Resolved
	if locked != nil {
		resolved = *locked
	} else {
		var err error
		resolved, err = a.resolve(ctx, s)
		if err != nil {
			return source.Result{}, err
		}
	}

	checksum, err := source.IntegrityChecksum(resolved.Integrity)
	if err != nil {
		return source.Result{}, err
	}

	root, hash, err := source.FetchURL(ctx, a.client, resolved.URL, checksum, resolved.SHA256, workspace, "tgz")
	if err != nil {
		return source.Result{}, err
	}

	resolved.SHA256 = hash
	return source.Result{
		Root:     filepath.Join(root, "package"),
		Resolved: resolved,
	}, nil
}

type versionMetadata struct {
	Name    string               `json:"name"`
	Version string               `json:"version"`
	Dist    distributionMetadata `json:"dist"`
}

type distributionMetadata struct {
	Tarball   string `json:"tarball"`
	Integrity string `json:"integrity"`
}

type packageMetadata struct {
	DistTags map[string]string          `json:"dist-tags"`
	Versions map[string]versionMetadata `json:"versions"`
}

func (a *Adapter) resolve(ctx context.Context, s source.Config) (source.Resolved, error) {
	endpoint := strings.TrimRight(s.Registry, "/") + "/" + url.PathEscape(s.Package)
	var metadata versionMetadata
	if source.ExactVersion(s.Version) {
		if err := a.metadata(ctx, endpoint+"/"+url.PathEscape(s.Version), &metadata); err != nil {
			return source.Resolved{}, err
		}
		if metadata.Version != s.Version {
			return source.Resolved{}, fmt.Errorf("registry returned a different version")
		}
	} else {
		var pack packageMetadata
		if err := a.metadata(ctx, endpoint, &pack); err != nil {
			return source.Resolved{}, err
		}

		v, ok := pack.DistTags[s.Version]
		if !ok {
			return source.Resolved{}, fmt.Errorf("npm dist-tag %q is missing", s.Version)
		}

		metadata, ok = pack.Versions[v]
		if !ok || metadata.Version != v {
			return source.Resolved{}, fmt.Errorf("npm tagged version metadata is missing or inconsistent")
		}
	}

	if metadata.Name != s.Package || !source.ExactVersion(metadata.Version) {
		return source.Resolved{}, fmt.Errorf("registry returned an unexpected package identity")
	}

	if err := source.ValidateHTTPURL(metadata.Dist.Tarball); err != nil {
		return source.Resolved{}, err
	}

	if _, err := source.IntegrityChecksum(metadata.Dist.Integrity); err != nil {
		return source.Resolved{}, err
	}

	return source.Resolved{
		URL:       metadata.Dist.Tarball,
		Version:   metadata.Version,
		Integrity: metadata.Dist.Integrity,
	}, nil
}

func (a *Adapter) metadata(ctx context.Context, endpoint string, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}

	request.Header.Set(headerAccept, metadataMediaType)
	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("npm registry returned HTTP %d", response.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxMetadataBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxMetadataBytes {
		return fmt.Errorf("registry metadata exceeds size limit")
	}

	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("invalid registry metadata: %w", err)
	}

	return nil
}
