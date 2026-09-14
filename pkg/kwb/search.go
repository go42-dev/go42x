package kwb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

const (
	MaxSearchLimit  = 100
	MaxListLimit    = 500
	MaxSearchOffset = 10000
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
	ChunkID        string   `json:"chunk_id"`
	ChunkStartLine int      `json:"chunk_start_line"`
	ChunkEndLine   int      `json:"chunk_end_line"`
	Symbols        []string `json:"symbols,omitempty"`
	SourceHash     string   `json:"source_hash"`
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
}

type SearchResponse struct {
	Results       []SearchResult `json:"results"`
	Total         uint64         `json:"total"`
	NextOffset    *int           `json:"next_offset,omitempty"`
	Generation    string         `json:"generation"`
	WindowLimited bool           `json:"window_limited,omitempty"`
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

func (m *indexManager) Search(ctx context.Context, options SearchOptions) (*SearchResponse, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if options.Limit == 0 {
		options.Limit = m.settings.SearchLimit
	}
	var matches []query.Query
	exact := bleve.NewTermQuery(options.Query)
	exact.SetField("symbols")
	exact.SetBoost(20)
	matches = append(matches, exact)
	for _, field := range []struct {
		name  string
		boost float64
	}{
		{"names", 6}, {"title", 5}, {"filename", 3}, {"code", 1}, {"prose", 1},
	} {
		match := bleve.NewMatchQuery(options.Query)
		match.SetField(field.name)
		match.SetBoost(field.boost)
		match.SetOperator(query.MatchQueryOperatorAnd)
		matches = append(matches, match)
	}
	clauses := []query.Query{bleve.NewDisjunctionQuery(matches...)}
	for field, value := range map[string]string{"kind": options.Kind, "language": options.Language} {
		if value != "" {
			term := bleve.NewTermQuery(value)
			term.SetField(field)
			clauses = append(clauses, term)
		}
	}
	if options.PathPrefix != "" {
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
	response := &SearchResponse{
		Results: []SearchResult{},
	}
	err := m.withSnapshot(ctx, func(s *snapshot) error {
		result, err := s.index.SearchInContext(ctx, request)
		if err != nil {
			return err
		}
		response.Total, response.Generation = result.Total, s.generation
		for _, hit := range result.Hits {
			if err := ctx.Err(); err != nil {
				return err
			}
			text := func(name string) string { value, _ := hit.Fields[name].(string); return value }
			start, _ := hit.Fields["start_line"].(float64)
			end, _ := hit.Fields["end_line"].(float64)
			snippet, first, last := excerpt(text("content"), options.Query)
			response.Results = append(response.Results, SearchResult{
				ChunkID:        hit.ID,
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
				SourceHash:     s.manifest.Files[text("path")].Hash,
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
		return nil
	})
	return response, err
}

func excerpt(content, queryText string) (string, int, int) {
	lines := sourceLines(content)
	terms := strings.FieldsFunc(
		strings.ToLower(queryText),
		func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) },
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
	if kind != "" && kind != "code" && kind != "documentation" && kind != "config" {
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
			if options.Kind != "" && options.Kind != file.Kind ||
				options.Language != "" && options.Language != file.Language ||
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
	if path == "" {
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
	err := m.withSnapshot(ctx, func(s *snapshot) error { root = s.manifest.Root; return nil })
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
	if strings.TrimSpace(options.Query) == "" {
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
	case []interface{}:
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
