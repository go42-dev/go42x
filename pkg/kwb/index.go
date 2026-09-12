package kwb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/gofrs/flock"
)

const schemaVersion = 2

type fileRecord struct {
	Hash     string `json:"hash"`
	Size     int64  `json:"size"`
	Chunks   int    `json:"chunks"`
	Lines    int    `json:"lines"`
	Kind     string `json:"kind"`
	Language string `json:"language"`
}

type manifest struct {
	Version   int                   `json:"version"`
	Root      string                `json:"root"`
	IndexType string                `json:"index_type"`
	BuiltAt   time.Time             `json:"built_at"`
	Files     map[string]fileRecord `json:"files"`
}

type snapshot struct {
	generation string
	index      bleve.Index
	manifest   *manifest
	lease      *flock.Flock
}

func (s *snapshot) close() error { return errors.Join(s.index.Close(), s.lease.Unlock()) }

type indexManager struct {
	logger   *slog.Logger
	settings *Settings
	mu       sync.RWMutex
	current  *snapshot
}

func newIndexManager(settings *Settings, logger *slog.Logger) *indexManager {
	return &indexManager{settings: settings, logger: logger}
}

func acquireLock(ctx context.Context, lock *flock.Flock, shared bool) error {
	var ok bool
	var err error
	if shared {
		ok, err = lock.TryRLockContext(ctx, 10*time.Millisecond)
	} else {
		ok, err = lock.TryLockContext(ctx, 10*time.Millisecond)
	}
	if err != nil {
		return err
	}
	if !ok {
		return ctx.Err()
	}
	return nil
}

func (m *indexManager) generation() (string, error) {
	data, err := os.ReadFile(filepath.Join(m.settings.IndexPath, "CURRENT"))
	if os.IsNotExist(err) {
		return "", fmt.Errorf(
			"index not found or requires rebuilding at %s; run 'go42x kwb --index %q' first: %w",
			m.settings.IndexPath,
			m.settings.IndexPath,
			err,
		)
	}
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(data))
	if !strings.HasPrefix(name, "gen-") || filepath.Base(name) != name {
		return "", fmt.Errorf("invalid index generation %q", name)
	}
	return name, nil
}

func readManifest(path string) (*manifest, error) {
	data, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var result manifest
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Files == nil || result.Root == "" {
		return nil, fmt.Errorf("invalid index manifest")
	}
	return &result, nil
}

// withSnapshot holds a shared in-process lease for the whole operation. Opening,
// replacing and closing an index are exclusive; searches on it run concurrently.
func (m *indexManager) withSnapshot(ctx context.Context, fn func(*snapshot) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		generation, err := m.generation()
		if err != nil {
			return err
		}
		m.mu.RLock()
		if m.current != nil && m.current.generation == generation {
			err := ctx.Err()
			if err == nil {
				err = fn(m.current)
			}
			m.mu.RUnlock()
			return err
		}
		m.mu.RUnlock()
		if err := m.refresh(ctx); err != nil {
			return err
		}
	}
}

func (m *indexManager) refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	catalog := flock.New(filepath.Join(m.settings.IndexPath, "catalog.lock"))
	if err := acquireLock(ctx, catalog, true); err != nil {
		return err
	}
	defer catalog.Unlock() //nolint:errcheck
	generation, err := m.generation()
	if err != nil {
		return err
	}
	if m.current != nil && m.current.generation == generation {
		return nil
	}
	directory := filepath.Join(m.settings.IndexPath, generation)
	lease := flock.New(filepath.Join(directory, "LEASE"))
	if err := acquireLock(ctx, lease, true); err != nil {
		return err
	}
	meta, err := readManifest(directory)
	if err != nil {
		_ = lease.Unlock()
		return err
	}
	if meta.Version != schemaVersion {
		_ = lease.Unlock()
		return fmt.Errorf("index format changed; run 'go42x kwb --rebuild' first")
	}
	index, err := bleve.OpenUsing(
		filepath.Join(directory, "data"),
		map[string]interface{}{"read_only": true, "bolt_timeout": "1s"},
	)
	if err != nil {
		_ = lease.Unlock()
		return fmt.Errorf("opening index: %w", err)
	}
	old := m.current
	m.current = &snapshot{generation, index, meta, lease}
	if old != nil {
		if err := old.close(); err != nil {
			m.logger.Warn("closing previous index", "error", err)
		}
	}
	return nil
}

func (m *indexManager) CloseIndex() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return nil
	}
	err := m.current.close()
	m.current = nil
	return err
}

func createMapping() (mapping.IndexMapping, error) {
	result := bleve.NewIndexMapping()
	if err := result.AddCustomAnalyzer("identifier", map[string]interface{}{
		"type": custom.Name, "tokenizer": unicode.Name, "token_filters": []string{lowercase.Name},
	}); err != nil {
		return nil, err
	}
	result.IndexDynamic, result.StoreDynamic = false, false
	doc := bleve.NewDocumentMapping()
	doc.Dynamic = false
	for _, name := range []string{"path", "kind", "language", "symbols"} {
		field := bleve.NewKeywordFieldMapping()
		field.Store, field.IncludeInAll, field.IncludeTermVectors = true, false, false
		doc.AddFieldMappingsAt(name, field)
	}
	for name, analyzer := range map[string]string{"title": "standard", "names": "identifier", "filename": "identifier", "code": "identifier", "prose": "standard"} {
		field := bleve.NewTextFieldMapping()
		field.Analyzer, field.Store, field.IncludeInAll, field.IncludeTermVectors = analyzer, name == "title", false, false
		doc.AddFieldMappingsAt(name, field)
	}
	content := bleve.NewTextFieldMapping()
	content.Index, content.Store, content.IncludeInAll, content.IncludeTermVectors = false, true, false, false
	doc.AddFieldMappingsAt("content", content)
	for _, name := range []string{"start_line", "end_line"} {
		field := bleve.NewNumericFieldMapping()
		field.Store, field.IncludeInAll = true, false
		doc.AddFieldMappingsAt(name, field)
	}
	result.DefaultMapping = doc
	return result, nil
}
