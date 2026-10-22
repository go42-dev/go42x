package kwb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	DefaultContentBytes = 16 * 1024
	MaxContentBytes     = 48 * 1024
	MaxItems            = 40
)

var ErrRootMismatch = errors.New("index root differs from requested project root")

type Service struct {
	logger   *slog.Logger
	index    indexAccessor
	catalog  catalogAccessor
	settings *Settings
}

func NewService(settings *Settings, opts ...Option) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("invalid settings: %w", err)
	}

	copy := *settings
	copy.ExtraExtensions = slices.Clone(settings.ExtraExtensions)
	copy.ExcludeDirs = slices.Clone(settings.ExcludeDirs)
	copy.ExcludeFiles = slices.Clone(settings.ExcludeFiles)
	copy.ContextDocs = slices.Clone(settings.ContextDocs)
	var err error
	copy.RootPath, err = canonicalPath(copy.RootPath)
	if err != nil {
		return nil, err
	}

	copy.IndexPath, err = canonicalPath(copy.IndexPath)
	if err != nil {
		return nil, err
	}

	svc := &Service{
		settings: &copy,
	}
	for _, opt := range opts {
		opt(svc)
	}
	if svc.logger == nil {
		svc.logger = slog.New(slog.DiscardHandler)
	}
	svc.logger = svc.logger.With(slog.String("component", "kwb"))

	svc.index = newIndexManager(svc.logger.With(slog.String("subsystem", "index_manager")), svc.settings)
	svc.catalog = newDocumentCatalog()

	return svc, nil
}

func (s *Service) BuildIndex(ctx context.Context, rootPath string) (BuildResult, error) {
	report, err := s.index.BuildIndex(ctx, rootPath)
	if err != nil {
		return report, fmt.Errorf("building index: %w", err)
	}

	s.logger.DebugContext(ctx, "Knowledge base updated",
		slog.Int("indexed", report.Indexed),
		slog.Int("unchanged", report.Unchanged),
		slog.Int("deleted", report.Deleted),
		slog.Int("files", report.Files),
		slog.Int("chunks", report.Chunks),
		slog.Duration("duration", report.Duration),
	)
	return report, nil
}

func (s *Service) Search(ctx context.Context, options SearchOptions) (*SearchResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()

	return s.index.Search(ctx, options)
}

func (s *Service) GetFile(ctx context.Context, path string, startLine, endLine int) (*FileContent, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()

	return s.index.GetFile(ctx, path, startLine, endLine)
}

func (s *Service) ListFiles(ctx context.Context, options ListOptions) (*FilesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()

	return s.index.ListFiles(ctx, options)
}

func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()

	return s.index.GetStats(ctx)
}

func (s *Service) Close() error {
	return s.index.CloseIndex()
}

// canonicalPath resolves existing symlinked ancestors even when the final index path does not
// exist yet, so source walking and index exclusion use the same absolute paths.
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	parent := filepath.Dir(abs)
	if parent == abs {
		return "", err
	}

	parent, err = canonicalPath(parent)
	if err != nil {
		return "", err
	}

	return filepath.Join(parent, filepath.Base(abs)), nil
}

// ProjectRoot resolves the indexed project without opening the search engine.
// An explicit root also permits live documentation reads when the index is broken.
// A valid index belonging to another explicit root is always rejected.
func (s *Service) ProjectRoot() (string, error) {
	root, err := s.index.ProjectRoot()
	if err != nil && s.settings.RequireRootMatch && !errors.Is(err, ErrRootMismatch) {
		return s.settings.RootPath, nil
	}

	return root, err
}

// Catalog reads current documentation independently of the search index lifecycle.
func (s *Service) Catalog(ctx context.Context) (*Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	root, err := s.ProjectRoot()
	if err != nil {
		return nil, err
	}

	return s.catalog.Load(ctx, root)
}

func (s *Service) GetDocument(ctx context.Context, id string, start, end int) (*DocumentResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()

	if len(strings.TrimSpace(id)) == 0 || len(id) > 128 {
		return nil, fmt.Errorf("id must contain 1 to 128 bytes")
	}

	catalog, err := s.Catalog(ctx)
	if err != nil {
		return nil, err
	}

	doc, err := catalog.ByID(id)
	if err != nil {
		return nil, err
	}

	source, err := ReadLines(doc.Content, start, end)
	if err != nil {
		return nil, err
	}

	replacements, _ := catalog.Replacements(doc)
	result := &DocumentResult{
		Document:      *doc,
		Source:        source,
		Truncated:     source.NextStartLine != nil,
		StatusMeaning: StatusMeaning(doc.Metadata),
		Replacements:  replacements,
		Diagnostics:   []Diagnostic{},
	}
	for _, diagnostic := range catalog.Diagnostics {
		if diagnostic.Path == doc.Path {
			result.Diagnostics = append(result.Diagnostics, diagnostic)
		}
	}
	// Cap metadata first, then reduce the source window until the encoded response
	// fits. This includes JSON escaping, not just raw source bytes.
	result.Document.Diagnostics = nil
	if len(result.Document.Headings) > 100 {
		result.Document.Headings = result.Document.Headings[:100]
		result.Truncated = true
	}
	if len(result.Document.Links) > 100 {
		result.Document.Links = result.Document.Links[:100]
		result.Truncated = true
	}
	if len(result.Diagnostics) > 100 {
		result.Diagnostics = result.Diagnostics[:100]
		result.Truncated = true
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		data, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if len(data) <= MaxResponseBytes {
			break
		}
		result.Truncated = true
		switch {
		case len(result.Document.Links) > 0:
			result.Document.Links = result.Document.Links[:len(result.Document.Links)/2]
		case len(result.Document.Headings) > 0:
			result.Document.Headings = result.Document.Headings[:len(result.Document.Headings)/2]
		case len(result.Diagnostics) > 0:
			result.Diagnostics = result.Diagnostics[:len(result.Diagnostics)/2]
		case result.Source.EndLine > result.Source.StartLine:
			result.Source, err = ReadLines(doc.Content, result.Source.StartLine, result.Source.EndLine-1)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("document metadata or single source line exceeds the encoded response limit")
		}
	}
	return result, ctx.Err()
}

// Impact maps explicit Markdown links and related/superseded_by edges. It makes
// no inference that unmapped code is unrelated to documentation.
func (s *Service) Impact(ctx context.Context, paths []string) (*ImpactResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()

	normalized, err := NormalizePaths(paths)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("at least one path is required")
	}

	catalog, err := s.Catalog(ctx)
	if err != nil {
		return nil, err
	}

	result, err := catalog.impact(ctx, normalized)
	if err != nil {
		return nil, err
	}

	if err := boundImpact(result); err != nil {
		return nil, err
	}

	return result, ctx.Err()
}

type ContextOptions struct {
	Paths    []string
	MaxBytes int
}

type ContextItem struct {
	Title         string     `json:"title,omitempty"`
	Path          string     `json:"path"`
	Hash          string     `json:"hash"`
	Document      *Metadata  `json:"document,omitempty"`
	Replacements  []Metadata `json:"replacements,omitempty"`
	StatusMeaning string     `json:"status_meaning,omitempty"`
	Reason        string     `json:"reason"`
	Evidence      []Evidence `json:"evidence,omitempty"`
	Source        Source     `json:"source"`
}

type ContextResult struct {
	Root         string        `json:"root"`
	Items        []ContextItem `json:"items"`
	Diagnostics  []Diagnostic  `json:"diagnostics"`
	ContentBytes int           `json:"content_bytes"`
	Truncated    bool          `json:"truncated"`
	Coverage     string        `json:"coverage"`
}

func readCandidate(ctx context.Context, root *os.Root, path string) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}

	data, err := readBounded(ctx, file, MaxFileBytes)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return nil, fmt.Errorf("source exceeds size limit or is not UTF-8")
	}

	return data, nil
}

func fitSource(source Source, budget int) (Source, bool) {
	if len(source.Content) <= budget {
		return source, false
	}

	prefix := source.Content[:budget]
	last := strings.LastIndexByte(prefix, '\n')
	if last < 0 {
		source.Content = ""
		return source, true
	}

	source.Content = prefix[:last+1]
	source.EndLine = source.StartLine + strings.Count(source.Content, "\n") - 1
	next := source.EndLine + 1
	source.NextStartLine = &next
	return source, true
}
