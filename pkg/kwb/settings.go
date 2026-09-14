package kwb

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const DefaultEntrypoint = "docs/README.md"

type Settings struct {
	// RequireRootMatch rejects indexes belonging to a different explicit project root.
	RequireRootMatch    bool
	Entrypoint          string
	DefaultContentBytes int
	RootPath            string
	IndexPath           string
	ExtraExtensions     []string
	ExcludeDirs         []string
	ExcludeFiles        []string
	ContextDocs         []string
	MaxFileSize         int
	BatchSize           int
	IndexType           string
	Rebuild             bool
	SearchTimeout       time.Duration
	SearchLimit         int
}

func NewSettings() *Settings {
	return &Settings{
		RootPath:            ".",
		Entrypoint:          DefaultEntrypoint,
		DefaultContentBytes: DefaultContentBytes,
		IndexPath:           ".go42x/kwb/index",
		MaxFileSize:         5 * 1024 * 1024,
		BatchSize:           1000,
		IndexType:           "scorch",
		SearchTimeout:       5 * time.Second,
		SearchLimit:         10,
	}
}

func (s *Settings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if s.RootPath == "" || s.IndexPath == "" {
		return fmt.Errorf("root and index paths are required")
	}
	if _, err := NormalizePaths([]string{s.Entrypoint}); err != nil {
		return fmt.Errorf("invalid documentation entrypoint: %w", err)
	}
	if ext := strings.ToLower(path.Ext(s.Entrypoint)); ext != ".md" && ext != ".markdown" {
		return fmt.Errorf("documentation entrypoint must be a Markdown file")
	}
	if s.DefaultContentBytes < 1 || s.DefaultContentBytes > MaxContentBytes {
		return fmt.Errorf("default content bytes must be between 1 and %d", MaxContentBytes)
	}
	if len(s.ContextDocs) > 8 {
		return fmt.Errorf("context docs allows at most 8 authored IDs")
	}
	for _, id := range s.ContextDocs {
		if strings.TrimSpace(id) == "" || len(id) > 128 {
			return fmt.Errorf("context document IDs must contain 1 to 128 bytes")
		}
	}
	if s.BatchSize <= 0 {
		return fmt.Errorf("batch size must be greater than 0")
	}
	if s.MaxFileSize <= 0 {
		return fmt.Errorf("max file size must be greater than 0")
	}
	if s.SearchLimit <= 0 || s.SearchLimit > MaxSearchLimit {
		return fmt.Errorf("search limit must be between 1 and %d", MaxSearchLimit)
	}
	if s.SearchTimeout <= 0 {
		return fmt.Errorf("search timeout must be greater than 0")
	}
	if s.IndexType != "scorch" && s.IndexType != "upsidedown" {
		return fmt.Errorf("invalid index type: %s (must be 'scorch' or 'upsidedown')", s.IndexType)
	}
	return nil
}

func (s *Settings) IndexExists() bool {
	_, err := os.Stat(filepath.Join(s.IndexPath, "CURRENT"))
	return err == nil
}

// SearchSettings configures a search and its output format.
type SearchSettings struct {
	KnowledgeBase *Settings
	Search        SearchOptions
	JSON          bool
}

func (s *SearchSettings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if err := s.KnowledgeBase.Validate(); err != nil {
		return err
	}
	if err := s.Search.Validate(); err != nil {
		return err
	}
	if s.Search.Limit == 0 {
		return fmt.Errorf("limit must be positive")
	}
	return nil
}

// ReadSettings configures a source read and its output format.
type ReadSettings struct {
	KnowledgeBase      *Settings
	Path               string
	StartLine, EndLine int
	JSON               bool
}

func (s *ReadSettings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if err := s.KnowledgeBase.Validate(); err != nil {
		return err
	}
	if s.Path == "" {
		return fmt.Errorf("path is required")
	}
	if s.StartLine < 1 || s.EndLine < s.StartLine || s.EndLine-s.StartLine >= MaxReadLines {
		return fmt.Errorf("request between 1 and %d lines using positive line numbers", MaxReadLines)
	}
	return nil
}

// StatsSettings configures index statistics and their output format.
type StatsSettings struct {
	KnowledgeBase *Settings
	JSON          bool
}

func (s *StatsSettings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	return s.KnowledgeBase.Validate()
}
