// Package confluence fetches Confluence Cloud pages as Atlassian Document Format (ADF) documents.
// @see https://developer.atlassian.com/cloud/confluence/rest/v2/api-group-page/#api-pages-id-get
package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go42-dev/go42x/internal/vendoring/source"
)

// PageFile is the name of the fetched page document in the source root.
const PageFile = "page.json"

const (
	headerAccept  = "Accept"
	mediaTypeJSON = "application/json"
)

// bodyFormat keeps code languages, panel types, and task states, which rendered HTML loses.
// Unlike rendered HTML, it does not change between requests for one version.
const bodyFormat = "atlas_doc_format"

const maxPageBytes = 16 << 20

type Adapter struct {
	client *http.Client
	lookup source.LookupFunc
}

func New(client *http.Client, lookup source.LookupFunc) *Adapter {
	return &Adapter{
		client: client,
		lookup: lookup,
	}
}

// Fetch resolves the current page version, or the locked one, and writes PageFile.
// The lock pins the version number; destination file hashes detect changed content.
func (a *Adapter) Fetch(
	ctx context.Context,
	cfg source.Config,
	locked *source.Resolved,
	workspace string,
) (source.Result, error) {
	client, err := source.Authorize(a.client, cfg, a.lookup)
	if err != nil {
		return source.Result{}, err
	}

	revision := ""
	if locked != nil {
		revision = locked.Revision
	}
	p, err := fetchPage(ctx, client, cfg, revision)
	if err != nil {
		return source.Result{}, err
	}

	document, err := p.document()
	if err != nil {
		return source.Result{}, err
	}

	root := filepath.Join(workspace, "files")
	if err := os.MkdirAll(root, 0700); err != nil {
		return source.Result{}, err
	}
	if err := os.WriteFile(filepath.Join(root, PageFile), document, 0600); err != nil {
		return source.Result{}, err
	}

	return source.Result{
		Root:     root,
		Resolved: source.Resolved{Revision: strconv.Itoa(p.version)},
	}, nil
}

// page is a fetched page version with its ADF body as JSON text.
type page struct {
	title   string
	version int
	body    string
}

type pageResponse struct {
	ID      string      `json:"id"`
	Title   string      `json:"title"`
	Version pageVersion `json:"version"`
	Body    pageBody    `json:"body"`
}

type pageVersion struct {
	Number int `json:"number"`
}

type pageBody struct {
	ADF *bodyValue `json:"atlas_doc_format"`
}

type bodyValue struct {
	Value string `json:"value"`
}

// fetchPage reads one page; an empty revision selects the current version.
func fetchPage(ctx context.Context, client *http.Client, s source.Config, revision string) (page, error) {
	query := url.Values{}
	query.Set("body-format", bodyFormat)
	if len(revision) > 0 {
		query.Set("version", revision)
	}
	endpoint := strings.TrimRight(s.URL, "/") + "/api/v2/pages/" + url.PathEscape(s.Page) + "?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return page{}, err
	}

	request.Header.Set(headerAccept, mediaTypeJSON)
	response, err := client.Do(request)
	if err != nil {
		return page{}, err
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return page{}, fmt.Errorf("page request returned HTTP %d; check the page ID and auth", response.StatusCode)
	default:
		return page{}, fmt.Errorf("page request returned HTTP %d", response.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxPageBytes+1))
	if err != nil {
		return page{}, err
	}
	if len(data) > maxPageBytes {
		return page{}, fmt.Errorf("page response exceeds size limit")
	}

	var r pageResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return page{}, fmt.Errorf("invalid page response: %w", err)
	}
	if r.ID != s.Page {
		return page{}, fmt.Errorf("page response describes a different page")
	}
	if !source.ValidRevision(strconv.Itoa(r.Version.Number)) {
		return page{}, fmt.Errorf("page response has no valid version")
	}
	if len(revision) > 0 && strconv.Itoa(r.Version.Number) != revision {
		return page{}, fmt.Errorf("page response has version %d, want %s", r.Version.Number, revision)
	}

	body := r.Body.ADF
	if body == nil {
		return page{}, fmt.Errorf("page response has no %s body", bodyFormat)
	}

	return page{
		title:   r.Title,
		version: r.Version.Number,
		body:    body.Value,
	}, nil
}

// document returns the ADF body, indented and headed by the page title. Unknown node
// fields and number precision are preserved.
func (p page) document() ([]byte, error) {
	decoder := json.NewDecoder(strings.NewReader(p.body))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid page document: %w", err)
	}
	if doc == nil || doc["type"] != "doc" {
		return nil, fmt.Errorf("page body is not an ADF document")
	}

	if len(strings.TrimSpace(p.title)) > 0 {
		content, _ := doc["content"].([]any)
		title := map[string]any{
			"type":  "heading",
			"attrs": map[string]any{"level": 1},
			"content": []any{
				map[string]any{
					"type": "text",
					"text": p.title,
				},
			},
		}
		doc["content"] = append([]any{title}, content...)
	}

	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}
