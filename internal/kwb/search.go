package kwb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
)

const (
	MaxSearchLimit  = 100
	MaxSearchOffset = 10000
	MaxListLimit    = 500
	MaxReadLines    = 500
	MaxReadBytes    = 64 * 1024
)

type SearchOptions struct {
	Query      string
	Kind       string
	Language   string
	PathPrefix string
	Limit      int
	Offset     int
}

type SearchResult struct {
	ChunkStartLine int      `json:"chunk_start_line"`
	ChunkEndLine   int      `json:"chunk_end_line"`
	Symbols        []string `json:"symbols,omitempty"`
	DocumentID     string   `json:"document_id,omitempty"`
	DocumentStatus string   `json:"document_status,omitempty"`
	Collection     string   `json:"collection,omitempty"`
	Related        []string `json:"related,omitempty"`
	SupersededBy   string   `json:"superseded_by,omitempty"`
	Path           string   `json:"path"`
	Kind           string   `json:"kind"`
	Language       string   `json:"language"`
	Title          string   `json:"title,omitempty"`
	StartLine      int      `json:"start_line"`
	EndLine        int      `json:"end_line"`
	Snippet        string   `json:"snippet"`
	Score          float64  `json:"score"`
	// Stale marks a result whose file changed or disappeared after indexing.
	Stale bool `json:"stale,omitempty"`
}

type SearchResponse struct {
	Results       []SearchResult `json:"results"`
	Total         uint64         `json:"total"`
	NextOffset    *int           `json:"next_offset,omitempty"`
	Generation    string         `json:"generation"`
	WindowLimited bool           `json:"window_limited,omitempty"`
	// Relaxed reports results that match only some query words, because none matched all of them.
	Relaxed bool `json:"relaxed,omitempty"`
}

type ListOptions struct {
	Kind       string
	Language   string
	PathPrefix string
	Limit      int
	Offset     int
}

type FileInfo struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Language string `json:"language"`
	Lines    int    `json:"lines"`
}

type FilesResponse struct {
	Files      []FileInfo `json:"files"`
	Total      int        `json:"total"`
	NextOffset *int       `json:"next_offset,omitempty"`
	Generation string     `json:"generation"`
}

type FileContent struct {
	Path          string `json:"path"`
	Content       string `json:"content"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	TotalLines    int    `json:"total_lines"`
	NextStartLine *int   `json:"next_start_line,omitempty"`
}

type Stats struct {
	DocumentCount int    `json:"document_count"`
	ChunkCount    int    `json:"chunk_count"`
	IndexPath     string `json:"index_path"`
	RootPath      string `json:"root_path"`
	Generation    string `json:"generation"`
}

type searchField struct {
	name  string
	boost float64
}

// searchFields weights the indexed fields a query term can match.
var searchFields = []searchField{
	{
		name:  "names",
		boost: 6,
	},
	{
		name:  "title",
		boost: 5,
	},
	{
		name:  "filename",
		boost: 3,
	},
	{
		name:  "code",
		boost: 1,
	},
	{
		name:  "prose",
		boost: 1,
	},
}

// maxRelaxedTerms bounds the size of a relaxed query built from a long question.
const maxRelaxedTerms = 32

func (m *indexManager) Search(ctx context.Context, options SearchOptions) (*SearchResponse, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if options.Limit == 0 {
		options.Limit = m.settings.SearchLimit
	}

	response := &SearchResponse{
		Results: []SearchResult{},
	}
	err := m.withSnapshot(ctx, func(s *snapshot) error {
		result, err := s.index.SearchInContext(ctx, searchRequest(strictQuery(options.Query), options))
		if err != nil {
			return err
		}
		// The strict total decides for every page, so offsets stay consistent across pages.
		if result.Total == 0 {
			if terms := queryTerms(s.index.Mapping(), options.Query); len(terms) >= 2 {
				relaxed, err := s.index.SearchInContext(ctx, searchRequest(relaxedQuery(terms), options))
				if err != nil {
					return err
				}
				if relaxed.Total > 0 {
					result, response.Relaxed = relaxed, true
				}
			}
		}

		response.Total, response.Generation = result.Total, s.generation
		for _, hit := range result.Hits {
			if err := ctx.Err(); err != nil {
				return err
			}

			text := func(name string) string {
				value, _ := hit.Fields[name].(string)
				return value
			}
			start, _ := hit.Fields["start_line"].(float64)
			end, _ := hit.Fields["end_line"].(float64)
			snippet, first, last := excerpt(text("content"), options.Query)
			response.Results = append(response.Results, SearchResult{
				ChunkStartLine: int(start),
				ChunkEndLine:   int(end),
				Symbols:        stringValues(hit.Fields["symbols"]),
				Path:           text("path"),
				Kind:           text("kind"),
				Language:       text("language"),
				Title:          text("title"),
				StartLine:      int(start) + first,
				EndLine:        int(start) + last,
				Snippet:        snippet,
				Score:          hit.Score,
				DocumentID:     text("document_id"),
				DocumentStatus: text("document_status"),
				Collection:     text("collection"),
				Related:        stringValues(hit.Fields["related"]),
				SupersededBy:   text("superseded_by"),
			})
		}
		if next := options.Offset + len(response.Results); next >= 0 && uint64(next) < result.Total {
			if next <= MaxSearchOffset {
				response.NextOffset = &next
			} else {
				response.WindowLimited = true
			}
		}
		return markStale(ctx, s, response.Results)
	})
	return response, err
}

// markStale flags results whose file changed or disappeared since the snapshot indexed it.
// It reads only the returned files.
func markStale(ctx context.Context, s *snapshot, results []SearchResult) error {
	if len(results) == 0 {
		return nil
	}
	root, err := os.OpenRoot(s.manifest.Root)
	if err != nil {
		for i := range results {
			results[i].Stale = true
		}
		return nil
	}
	defer root.Close() //nolint:errcheck
	changed := map[string]bool{}
	for i := range results {
		path := results[i].Path
		stale, checked := changed[path]
		if !checked {
			stale = fileChanged(ctx, root, path, s.manifest.Files[path])
			changed[path] = stale
		}
		results[i].Stale = stale
	}
	return ctx.Err()
}

// fileChanged compares a file's current size, then its hash, with its index record.
func fileChanged(ctx context.Context, root *os.Root, path string, record fileRecord) bool {
	file, err := root.Open(filepath.FromSlash(path))
	if err != nil {
		return true
	}
	defer file.Close() //nolint:errcheck
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != record.Size {
		return true
	}
	data, err := readBounded(ctx, file, int(record.Size))
	return err != nil || Hash(data) != record.Hash
}

// strictQuery matches an exact symbol, or every word of text within one field.
func strictQuery(text string) query.Query {
	exact := bleve.NewTermQuery(text)
	exact.SetField("symbols")
	exact.SetBoost(20)
	matches := []query.Query{exact}
	for _, field := range searchFields {
		match := bleve.NewMatchQuery(text)
		match.SetField(field.name)
		match.SetBoost(field.boost)
		match.SetOperator(query.MatchQueryOperatorAnd)
		matches = append(matches, match)
	}
	return bleve.NewDisjunctionQuery(matches...)
}

// relaxedQuery matches chunks that contain at least half of the terms, and at least two, in any field.
func relaxedQuery(terms []string) query.Query {
	perTerm := make([]query.Query, 0, len(terms))
	for _, term := range terms {
		fields := make([]query.Query, 0, len(searchFields))
		for _, field := range searchFields {
			match := bleve.NewMatchQuery(term)
			match.SetField(field.name)
			match.SetBoost(field.boost)
			fields = append(fields, match)
		}
		perTerm = append(perTerm, bleve.NewDisjunctionQuery(fields...))
	}
	anyTerms := bleve.NewDisjunctionQuery(perTerm...)
	anyTerms.SetMin(float64(max(2, (len(terms)+1)/2)))
	return anyTerms
}

// queryTerms returns the distinct words of text after prose analysis, which drops stop words.
func queryTerms(indexMapping mapping.IndexMapping, text string) []string {
	analyzer := indexMapping.AnalyzerNamed("standard")
	if analyzer == nil {
		return nil
	}
	var terms []string
	for _, token := range analyzer.Analyze([]byte(text)) {
		term := string(token.Term)
		if !slices.Contains(terms, term) {
			terms = append(terms, term)
		}
		if len(terms) == maxRelaxedTerms {
			break
		}
	}
	return terms
}

// searchRequest applies the filters, page, and returned fields of options to match.
func searchRequest(match query.Query, options SearchOptions) *bleve.SearchRequest {
	clauses := []query.Query{match}
	if len(options.Kind) > 0 {
		term := bleve.NewTermQuery(options.Kind)
		term.SetField("kind")
		clauses = append(clauses, term)
	}
	if len(options.Language) > 0 {
		term := bleve.NewTermQuery(options.Language)
		term.SetField("language")
		clauses = append(clauses, term)
	}
	if len(options.PathPrefix) > 0 {
		prefix := bleve.NewPrefixQuery(filepath.ToSlash(options.PathPrefix))
		prefix.SetField("path")
		clauses = append(clauses, prefix)
	}
	request := bleve.NewSearchRequestOptions(
		bleve.NewConjunctionQuery(clauses...),
		options.Limit,
		options.Offset,
		false,
	)
	request.SortBy([]string{"-_score", "_id"})
	request.Fields = []string{
		"symbols",
		"path",
		"kind",
		"language",
		"title",
		"content",
		"start_line",
		"end_line",
		"document_id",
		"document_status",
		"collection",
		"related",
		"superseded_by",
	}
	return request
}

func excerpt(content, queryText string) (string, int, int) {
	lines := sourceLines(content)
	if lines == nil {
		return "", 0, 0
	}

	terms := strings.FieldsFunc(
		strings.ToLower(queryText),
		func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		},
	)
	start := 0
search:
	for i, line := range lines {
		for _, term := range terms {
			if strings.Contains(strings.ToLower(line), term) {
				start = max(0, i-2)
				break search
			}
		}
	}
	end := min(len(lines), start+12)
	var result strings.Builder
	last := start
	for i := start; i < end; i++ {
		if result.Len() > 0 {
			result.WriteByte('\n')
		}
		remaining := 2000 - result.Len()
		if remaining <= 0 {
			break
		}
		result.WriteString(utf8Prefix(lines[i], remaining))
		last = i
		if result.Len() >= 2000 {
			break
		}
	}
	return result.String(), start, last
}

func validKind(kind string) error {
	if len(kind) > 0 && kind != KindCode && kind != KindDocumentation && kind != KindConfig {
		return fmt.Errorf("kind must be code, documentation, or config")
	}

	return nil
}

func (m *indexManager) ListFiles(ctx context.Context, options ListOptions) (*FilesResponse, error) {
	if options.Limit == 0 {
		options.Limit = 100
	}
	if options.Limit < 1 || options.Limit > MaxListLimit {
		return nil, fmt.Errorf("limit must be between 1 and %d", MaxListLimit)
	}
	if options.Offset < 0 {
		return nil, fmt.Errorf("offset cannot be negative")
	}

	if err := validKind(options.Kind); err != nil {
		return nil, err
	}

	response := &FilesResponse{
		Files: []FileInfo{},
	}
	err := m.withSnapshot(ctx, func(s *snapshot) error {
		var paths []string
		for path, file := range s.manifest.Files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(options.Kind) > 0 && options.Kind != file.Kind ||
				len(options.Language) > 0 && options.Language != file.Language ||
				!strings.HasPrefix(path, filepath.ToSlash(options.PathPrefix)) {
				continue
			}

			paths = append(paths, path)
		}
		sort.Strings(paths)
		response.Total, response.Generation = len(paths), s.generation
		start := min(options.Offset, len(paths))
		end := start + min(options.Limit, len(paths)-start)
		for _, path := range paths[start:end] {
			file := s.manifest.Files[path]
			response.Files = append(response.Files, FileInfo{
				path,
				file.Kind,
				file.Language,
				file.Lines,
			})
		}
		if end < len(paths) {
			response.NextOffset = &end
		}
		return ctx.Err()
	})
	return response, err
}

func (m *indexManager) GetFile(ctx context.Context, path string, start, end int) (*FileContent, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("path is required")
	}
	if start < 0 || end < 0 {
		return nil, fmt.Errorf("line numbers cannot be negative")
	}
	if start == 0 {
		start = 1
	}
	if end == 0 {
		end = start + 199
	}
	if end < start || end-start >= MaxReadLines {
		return nil, fmt.Errorf("request between 1 and %d lines", MaxReadLines)
	}

	root := m.settings.RootPath
	err := m.withSnapshot(ctx, func(s *snapshot) error {
		root = s.manifest.Root
		return nil
	})
	if err != nil {
		// Only an absent CURRENT pointer permits unindexed reads. A missing file
		// inside an existing generation is corruption, not permission to change roots.
		if _, generationErr := m.generation(); !errors.Is(generationErr, os.ErrNotExist) {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if filepath.IsAbs(path) {
		path, err = filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer rootFS.Close() //nolint:errcheck
	file, err := rootFS.Open(filepath.FromSlash(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path must refer to a regular file")
	}

	data, err := readBounded(ctx, file, m.settings.MaxFileSize)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return nil, fmt.Errorf("file must contain UTF-8 text")
	}

	source, err := ReadLines(string(data), start, end)
	if err != nil {
		return nil, err
	}

	return &FileContent{
		Path:          filepath.ToSlash(filepath.Clean(path)),
		Content:       source.Content,
		StartLine:     source.StartLine,
		EndLine:       source.EndLine,
		TotalLines:    source.TotalLines,
		NextStartLine: source.NextStartLine,
	}, ctx.Err()
}

func (m *indexManager) GetStats(ctx context.Context) (*Stats, error) {
	var stats *Stats
	err := m.withSnapshot(ctx, func(s *snapshot) error {
		stats = &Stats{
			DocumentCount: len(s.manifest.Files),
			IndexPath:     m.settings.IndexPath,
			RootPath:      s.manifest.Root,
			Generation:    s.generation,
		}
		for _, file := range s.manifest.Files {
			stats.ChunkCount += file.Chunks
		}
		return ctx.Err()
	})
	return stats, err
}

// Validate checks search inputs without opening the index. Zero limit uses the service default.
func (options SearchOptions) Validate() error {
	if len(strings.TrimSpace(options.Query)) == 0 {
		return fmt.Errorf("query is required")
	}
	if len(options.Query) > 4096 {
		return fmt.Errorf("query exceeds 4096 bytes")
	}
	if options.Limit == 0 {
		options.Limit = 10
	}
	if options.Limit < 1 || options.Limit > MaxSearchLimit {
		return fmt.Errorf("limit must be between 1 and %d", MaxSearchLimit)
	}
	if options.Offset < 0 || options.Offset > MaxSearchOffset {
		return fmt.Errorf("offset must be between 0 and %d; narrow the search with filters", MaxSearchOffset)
	}

	if err := validKind(options.Kind); err != nil {
		return err
	}

	return nil
}

func stringValues(value any) []string {
	switch value := value.(type) {
	case string:
		return []string{value}
	case []any:
		result := []string{}
		for _, v := range value {
			if text, ok := v.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}
