package kwb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrRootMismatch = errors.New("index root differs from requested project root")

type Service struct {
	logger   *slog.Logger
	settings *Settings
	index    indexAccessor
	catalog  catalogAccessor
}

func NewService(settings *Settings, opts ...Option) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("invalid settings: %w", err)
	}
	copy := *settings
	copy.ExtraExtensions = slices.Clone(settings.ExtraExtensions)
	copy.ExcludeDirs = slices.Clone(settings.ExcludeDirs)
	var err error
	copy.RootPath, err = canonicalPath(copy.RootPath)
	if err != nil {
		return nil, err
	}
	copy.Entrypoint = path.Clean(copy.Entrypoint)
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
	svc.index = newIndexManager(svc.settings, svc.logger.With("component", "index_manager"))
	svc.catalog = newDocumentCatalog()
	return svc, nil
}

func (s *Service) BuildIndex(ctx context.Context, rootPath string) (BuildResult, error) {
	report, err := s.index.BuildIndex(ctx, rootPath)
	if err != nil {
		return report, fmt.Errorf("building index: %w", err)
	}
	s.logger.InfoContext(ctx, "Knowledge base updated", "indexed", report.Indexed, "unchanged", report.Unchanged,
		"deleted", report.Deleted, "files", report.Files, "chunks", report.Chunks, "duration", report.Duration)
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

func (s *Service) Close() error { return s.index.CloseIndex() }

// Resolve existing symlinked ancestors even when the final index path does not
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
	root, err := s.ProjectRoot()
	if err != nil {
		return nil, err
	}
	return s.catalog.Load(ctx, root, s.settings.Entrypoint)
}

func (s *Service) GetDocument(ctx context.Context, id string, start, end int) (*DocumentResult, error) {
	if strings.TrimSpace(id) == "" || len(id) > 128 {
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

func (s *Service) Impact(ctx context.Context, paths []string) (*ImpactResult, error) {
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
	result := catalog.Impact(normalized)
	if err := boundImpact(result); err != nil {
		return nil, err
	}
	return result, ctx.Err()
}

// Impact maps explicit Markdown links and related/superseded_by edges. It makes
// no inference that unmapped code is unrelated to documentation.
const (
	DefaultContentBytes = 16 * 1024
	MaxContentBytes     = 48 * 1024
	MaxItems            = 40
)

type ContextOptions struct {
	Task     string
	Paths    []string
	MaxBytes int
}

type ContextItem struct {
	Path          string     `json:"path"`
	Hash          string     `json:"hash"`
	Document      *Metadata  `json:"document,omitempty"`
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

type candidate struct {
	doc                *Document
	path, reason, hash string
	start, end         int
	evidence           []Evidence
}

func (s *Service) Context(ctx context.Context, options ContextOptions) (*ContextResult, error) {
	if strings.TrimSpace(options.Task) == "" || len(options.Task) > 4096 {
		return nil, fmt.Errorf("task must contain 1 to 4096 bytes")
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = s.settings.DefaultContentBytes
	}
	if options.MaxBytes < 1 || options.MaxBytes > MaxContentBytes {
		return nil, fmt.Errorf("max_bytes must be between 1 and %d", MaxContentBytes)
	}
	paths, err := NormalizePaths(options.Paths)
	if err != nil {
		return nil, err
	}
	projectRoot, err := s.ProjectRoot()
	if err != nil {
		return nil, err
	}
	catalog, err := s.catalog.Load(ctx, projectRoot, s.settings.Entrypoint)
	if err != nil {
		return nil, err
	}
	result := &ContextResult{
		Root:        projectRoot,
		Items:       []ContextItem{},
		Diagnostics: slices.Clone(catalog.Diagnostics),
		Coverage:    "Live documentation and keyword-ranked index candidates; retrieved source hashes verified, index-wide freshness unchecked",
	}
	candidates := []candidate{}
	seen := map[string]bool{}
	add := func(c candidate) {
		if !seen[c.path] {
			seen[c.path] = true
			candidates = append(candidates, c)
		}
	}
	if doc := catalog.ByPath(s.settings.Entrypoint); doc != nil {
		add(candidate{
			doc:    doc,
			path:   doc.Path,
			reason: "bootstrap",
			start:  doc.BodyStartLine,
		})
	}
	impact := catalog.Impact(paths)
	for _, hit := range impact.Documents {
		if doc := catalog.ByPath(hit.Path); doc != nil {
			add(candidate{
				doc:      doc,
				path:     hit.Path,
				reason:   hit.Reason,
				evidence: hit.Evidence,
			})
		}
	}
	terms := keywords(options.Task)
	authoring := isAuthoring(terms)
	type ranked struct {
		doc   *Document
		score int
	}
	rankedDocs := []ranked{}
	for _, doc := range catalog.Documents {
		if doc.Collection == "templates" && !authoring {
			continue
		}
		score := relevance(doc, terms)
		if score > 0 {
			rankedDocs = append(rankedDocs, ranked{
				doc,
				score,
			})
		}
	}
	slices.SortStableFunc(rankedDocs, func(a, b ranked) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.doc.Path, b.doc.Path)
	})
	for _, rank := range rankedDocs[:min(len(rankedDocs), 4)] {
		add(candidate{
			doc:    rank.doc,
			path:   rank.doc.Path,
			reason: "task_keywords",
		})
	}
	stats, err := s.GetStats(ctx)
	if errors.Is(err, ErrRootMismatch) {
		return nil, err
	}
	if err == nil && stats.RootPath != projectRoot {
		return nil, fmt.Errorf("%w: project root changed during context retrieval", ErrRootMismatch)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result.Diagnostics = append(
			result.Diagnostics,
			Diagnostic{
				Code:    "index_unavailable",
				Message: "Index missing, unreadable, or incompatible; rebuild with go42x kwb build --rebuild",
			},
		)
		result.Coverage = "Live documentation only; code search coverage is reduced"
	} else {
		// Several short queries tolerate natural-language tasks without requiring
		// every task word to occur in one indexed field. Bound queries and candidates.
		queries := []string{}
		if len(terms) > 0 {
			queries = append(queries, strings.Join(terms, " "))
		}
		queries = append(queries, terms[:min(len(terms), 6)]...)
		for _, query := range queries {
			response, err := s.Search(ctx, SearchOptions{
				Query: query,
				Limit: 10,
			})
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				result.Diagnostics = append(
					result.Diagnostics,
					Diagnostic{
						Code:    "search_failed",
						Message: "A keyword search failed; code coverage may be reduced",
					},
				)
				break
			}
			if response.Generation != stats.Generation {
				result.Diagnostics = append(
					result.Diagnostics,
					Diagnostic{
						Code:    "index_changed",
						Message: "Index changed during retrieval; returned candidates are verified against live source",
					},
				)
			}
			for _, hit := range response.Results {
				if doc := catalog.ByPath(hit.Path); doc != nil {
					if doc.Collection == "templates" && !authoring {
						continue
					}
					if doc.Hash != hit.SourceHash {
						result.Diagnostics = append(
							result.Diagnostics,
							Diagnostic{
								Code:    "stale_candidate",
								Path:    hit.Path,
								Message: "Indexed document differs from current source; using the live document",
							},
						)
					}
					add(candidate{
						doc:    doc,
						path:   hit.Path,
						reason: "index_search",
					})
				} else {
					add(
						candidate{
							path:   hit.Path,
							reason: "index_search",
							hash:   hit.SourceHash,
							start:  hit.StartLine,
							end:    hit.EndLine,
						},
					)
				}
			}
		}
	}
	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close() //nolint:errcheck
	remaining := options.MaxBytes
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if remaining == 0 || len(result.Items) >= MaxItems {
			result.Truncated = true
			break
		}
		var content, hash string
		if c.doc != nil {
			content = c.doc.Content
			hash = c.doc.Hash
			if c.start == 0 {
				c.start, c.end = bestRange(c.doc, terms)
			}
		} else {
			data, err := readCandidate(root, c.path)
			if err != nil {
				result.Diagnostics = append(
					result.Diagnostics,
					Diagnostic{
						Code:    "source_unavailable",
						Path:    c.path,
						Message: "Indexed source could not be read",
					},
				)
				continue
			}
			hash = Hash(data)
			if hash != c.hash {
				result.Diagnostics = append(
					result.Diagnostics,
					Diagnostic{
						Code:    "stale_candidate",
						Path:    c.path,
						Message: "Indexed source changed; stale snippet omitted, rebuild with go42x kwb build",
					},
				)
				continue
			}
			content = string(data)
		}
		source, err := ReadLines(content, c.start, c.end)
		if err != nil {
			result.Diagnostics = append(
				result.Diagnostics,
				Diagnostic{
					Code:    "source_range_unavailable",
					Path:    c.path,
					Message: err.Error(),
				},
			)
			continue
		}
		// Fair per-document allocation leaves room for bootstrap and task-specific
		// evidence. Continue through whole lines; oversized lines are explicitly skipped.
		budget := min(remaining, 2400)
		if c.reason == "bootstrap" {
			budget = min(budget, 1200)
		}
		source, cut := fitSource(source, budget)
		if cut || source.NextStartLine != nil {
			result.Truncated = true
		}
		if source.Content == "" {
			result.Diagnostics = append(
				result.Diagnostics,
				Diagnostic{
					Code:    "content_budget",
					Path:    c.path,
					Message: "The first source line does not fit the remaining content budget",
				},
			)
			continue
		}
		item := ContextItem{
			Path:     c.path,
			Hash:     hash,
			Reason:   c.reason,
			Evidence: c.evidence,
			Source:   source,
		}
		if c.doc != nil {
			metadata := c.doc.Metadata
			item.Document = &metadata
			item.StatusMeaning = StatusMeaning(metadata)
		}
		result.Items = append(result.Items, item)
		remaining -= len(source.Content)
		result.ContentBytes += len(source.Content)
	}
	if len(result.Diagnostics) > 100 {
		result.Diagnostics = result.Diagnostics[:100]
		result.Truncated = true
	}
	for {
		data, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if len(data) <= MaxResponseBytes {
			break
		}
		result.Truncated = true
		if len(result.Diagnostics) > 0 {
			result.Diagnostics = result.Diagnostics[:len(result.Diagnostics)-1]
		} else if len(result.Items) > 0 {
			last := result.Items[len(result.Items)-1]
			result.ContentBytes -= len(last.Source.Content)
			result.Items = result.Items[:len(result.Items)-1]
		} else {
			return nil, fmt.Errorf("context response exceeds limit")
		}
	}
	return result, ctx.Err()
}

func readCandidate(root *os.Root, path string) ([]byte, error) {
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
	data, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes || !utf8.Valid(data) {
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

func keywords(task string) []string {
	stop := " a an and are as at be can change do does for from how i in implement is it me of on or please should the this to update we with would "
	words := strings.FieldsFunc(
		strings.ToLower(task),
		func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' },
	)
	result := []string{}
	for _, word := range words {
		if len(word) > 1 && !strings.Contains(stop, " "+word+" ") && !slices.Contains(result, word) {
			result = append(result, word)
			if len(result) == 12 {
				break
			}
		}
	}
	return result
}

func relevance(doc *Document, terms []string) int {
	score := 0
	title := strings.ToLower(doc.Title + " " + doc.ID)
	body := strings.ToLower(doc.Content)
	for _, term := range terms {
		if strings.Contains(title, term) {
			score += 5
		}
		if strings.Contains(body, term) {
			score++
		}
	}
	return score
}

func bestRange(doc *Document, terms []string) (int, int) {
	start := doc.BodyStartLine
	score := 0
	lines := strings.Split(doc.Content, "\n")
	for _, heading := range doc.Headings {
		current := 0
		title := strings.ToLower(heading.Title)
		for _, term := range terms {
			if strings.Contains(title, term) {
				current += 4
			}
			end := min(heading.EndLine, len(lines))
			if strings.Contains(strings.ToLower(strings.Join(lines[heading.StartLine-1:end], "\n")), term) {
				current++
			}
		}
		if current > score {
			score = current
			start = heading.StartLine
		}
	}
	return start, min(start+79, doc.TotalLines)
}

func isAuthoring(terms []string) bool {
	hasDocs, hasAction := false, false
	for _, term := range terms {
		if slices.Contains(
			[]string{"docs", "documentation", "requirement", "requirements", "decision", "adr", "handbook"},
			term,
		) {
			hasDocs = true
		}
		if slices.Contains([]string{"write", "author", "create", "new", "template"}, term) {
			hasAction = true
		}
	}
	return hasDocs && hasAction
}
