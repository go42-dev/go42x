package kwb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/blevesearch/bleve/v2/index/upsidedown"
	"github.com/blevesearch/bleve/v2/index/upsidedown/store/boltdb"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/gofrs/flock"
)

const schemaVersion = 4

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
	return &indexManager{
		settings: settings,
		logger:   logger,
	}
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
			"index not found or requires rebuilding at %s; run 'go42x kwb build --index %q' first: %w",
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
	catalog := flock.New(filepath.Join(m.settings.IndexPath, "catalog.lock"), flock.SetFlag(os.O_RDONLY))
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
	lease := flock.New(filepath.Join(directory, "LEASE"), flock.SetFlag(os.O_RDONLY))
	if err := acquireLock(ctx, lease, true); err != nil {
		return err
	}
	meta, err := readManifest(directory)
	if err != nil {
		_ = lease.Unlock()
		return err
	}
	if m.settings.RequireRootMatch && meta.Root != m.settings.RootPath {
		_ = lease.Unlock()
		return fmt.Errorf("%w: index %q, requested %q", ErrRootMismatch, meta.Root, m.settings.RootPath)
	}
	if meta.Version != schemaVersion {
		_ = lease.Unlock()
		return fmt.Errorf("index format changed; run 'go42x kwb build --rebuild' first")
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
	m.current = &snapshot{
		generation,
		index,
		meta,
		lease,
	}
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
	for _, name := range []string{"path", "kind", "language", "symbols", "document_id", "document_status", "collection", "related", "superseded_by"} {
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

// ProjectRoot resolves the indexed project without opening the search engine.
// This also works during an index-schema upgrade; absent indexes use RootPath.
func (m *indexManager) ProjectRoot() (string, error) {
	generation, err := m.generation()
	if errors.Is(err, os.ErrNotExist) {
		return m.settings.RootPath, nil
	}
	if err != nil {
		return "", err
	}
	meta, err := readManifest(filepath.Join(m.settings.IndexPath, generation))
	if err != nil {
		return "", err
	}
	if m.settings.RequireRootMatch && meta.Root != m.settings.RootPath {
		return "", fmt.Errorf("%w: index %q, requested %q", ErrRootMismatch, meta.Root, m.settings.RootPath)
	}
	return meta.Root, nil
}

type BuildResult struct {
	Indexed   int           `json:"indexed"`
	Unchanged int           `json:"unchanged"`
	Deleted   int           `json:"deleted"`
	Files     int           `json:"files"`
	Chunks    int           `json:"chunks"`
	Rebuilt   bool          `json:"rebuilt"`
	Duration  time.Duration `json:"duration"`
}

func (m *indexManager) BuildIndex(ctx context.Context, root string) (report BuildResult, retErr error) {
	started := time.Now()
	defer func() { report.Duration = time.Since(started) }()
	root, err := filepath.Abs(root)
	if err != nil {
		return report, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return report, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return report, err
	}
	if !info.IsDir() {
		return report, fmt.Errorf("index root must be a directory")
	}
	if root == m.settings.IndexPath {
		return report, fmt.Errorf("index path must differ from source root")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := os.MkdirAll(m.settings.IndexPath, 0755); err != nil {
		return report, err
	}
	writer := flock.New(filepath.Join(m.settings.IndexPath, "build.lock"))
	if err := acquireLock(ctx, writer, false); err != nil {
		return report, err
	}
	defer func() { retErr = errors.Join(retErr, writer.Unlock()) }()

	generation, err := m.generation()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return report, err
	}
	var previous *manifest
	if err == nil {
		previous, err = readManifest(filepath.Join(m.settings.IndexPath, generation))
		if err != nil {
			return report, err
		}
	}
	report.Rebuilt = m.settings.Rebuild || previous == nil || previous.Version != schemaVersion ||
		previous.Root != root ||
		previous.IndexType != m.settings.IndexType
	if report.Rebuilt {
		previous = nil
	}
	meta := &manifest{
		Version:   schemaVersion,
		Root:      root,
		IndexType: m.settings.IndexType,
		Files:     map[string]fileRecord{},
	}
	var directory string
	var index bleve.Index
	var batch *bleve.Batch
	defer func() {
		if index != nil {
			retErr = errors.Join(retErr, index.Close())
		}
		if directory != "" {
			retErr = errors.Join(retErr, os.RemoveAll(directory))
		}
	}()
	prepare := func() error {
		if index != nil {
			return nil
		}
		directory, err = os.MkdirTemp(m.settings.IndexPath, "gen-")
		if err != nil {
			return err
		}
		// Readers only open existing lock files; preparation owns their creation.
		if err := os.WriteFile(filepath.Join(directory, "LEASE"), nil, 0600); err != nil {
			return err
		}
		dataPath := filepath.Join(directory, "data")
		if previous != nil {
			if err := cloneIndex(
				ctx,
				filepath.Join(m.settings.IndexPath, generation, "data"),
				dataPath,
				m.settings.IndexType == "scorch",
			); err != nil {
				return err
			}
			index, err = bleve.OpenUsing(dataPath, map[string]interface{}{"bolt_timeout": "1s"})
		} else {
			indexMapping, mappingErr := createMapping()
			if mappingErr != nil {
				return mappingErr
			}
			backend := m.settings.IndexType
			if backend == "upsidedown" {
				backend = upsidedown.Name
			}
			index, err = bleve.NewUsing(
				dataPath,
				indexMapping,
				backend,
				boltdb.Name,
				nil,
			)
		}
		if err != nil {
			index = nil
			return err
		}
		batch = index.NewBatch()
		return nil
	}
	flush := func(force bool) error {
		if batch.Size() == 0 || (!force && batch.Size() < m.settings.BatchSize) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := index.Batch(batch); err != nil {
			return err
		}
		batch = index.NewBatch()
		return nil
	}
	err = m.walkFiles(ctx, root, func(path string, info os.FileInfo, data []byte) error {
		digest := Hash(data)
		var old fileRecord
		if previous != nil {
			old = previous.Files[path]
		}
		if old.Hash == digest {
			meta.Files[path] = old
			report.Unchanged++
			return nil
		}
		if err := prepare(); err != nil {
			return err
		}
		docs := chunkFile(path, data)
		for i := len(docs); i < old.Chunks; i++ {
			batch.Delete(chunkID(path, i))
			if err := flush(false); err != nil {
				return err
			}
		}
		for i, doc := range docs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := batch.Index(chunkID(path, i), doc); err != nil {
				return err
			}
			if err := flush(false); err != nil {
				return err
			}
		}
		kind, language := fileKind(path)
		meta.Files[path] = fileRecord{
			digest,
			info.Size(),
			len(docs),
			len(sourceLines(string(data))),
			kind,
			language,
		}
		report.Indexed++
		return nil
	})
	if err != nil {
		return report, fmt.Errorf("scanning source files: %w", err)
	}
	if previous != nil {
		for path, record := range previous.Files {
			if _, exists := meta.Files[path]; exists {
				continue
			}
			if err := prepare(); err != nil {
				return report, err
			}
			for i := 0; i < record.Chunks; i++ {
				batch.Delete(chunkID(path, i))
				if err := flush(false); err != nil {
					return report, err
				}
			}
			report.Deleted++
		}
	}
	for _, record := range meta.Files {
		report.Chunks += record.Chunks
	}
	report.Files = len(meta.Files)
	if report.Indexed == 0 && report.Deleted == 0 && !report.Rebuilt {
		return report, ctx.Err()
	}
	if err := prepare(); err != nil {
		return report, err
	}
	if err := flush(true); err != nil {
		return report, err
	}
	if err := index.Close(); err != nil {
		index = nil
		return report, err
	}
	index = nil
	meta.BuiltAt = time.Now().UTC()
	data, err := json.Marshal(meta)
	if err != nil {
		return report, err
	}
	if err := writeSynced(filepath.Join(directory, "manifest.json"), data); err != nil {
		return report, err
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := m.publish(ctx, filepath.Base(directory)); err != nil {
		return report, err
	}
	directory = "" // Published generations are owned by the catalog.
	return report, nil
}

// Scorch's .zap segments are immutable. Hard-linking them avoids copying the
// corpus on small updates; mutable metadata is always copied independently.
func cloneIndex(ctx context.Context, source, destination string, scorch bool) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if scorch && filepath.Ext(path) == ".zap" {
			if err := os.Link(path, target); err == nil {
				return nil
			}
		}
		return copyFile(ctx, path, target)
	})
}

func copyFile(ctx context.Context, source, target string) (retErr error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, output.Close()) }()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := input.Read(buffer)
		if n > 0 {
			if _, err := output.Write(buffer[:n]); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return output.Sync()
		}
		if err != nil {
			return err
		}
	}
}

func writeSynced(path string, data []byte) (retErr error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func (m *indexManager) publish(ctx context.Context, generation string) error {
	catalog := flock.New(filepath.Join(m.settings.IndexPath, "catalog.lock"))
	if err := acquireLock(ctx, catalog, false); err != nil {
		return err
	}
	defer catalog.Unlock() //nolint:errcheck
	temporary, err := os.CreateTemp(m.settings.IndexPath, ".current-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	_, writeErr := temporary.WriteString(generation + "\n")
	err = errors.Join(writeErr, temporary.Sync(), temporary.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), filepath.Join(m.settings.IndexPath, "CURRENT")); err != nil {
		return err
	}
	// Publication succeeded. Cleanup failures cannot invalidate the new generation.
	entries, err := os.ReadDir(m.settings.IndexPath)
	if err != nil {
		m.logger.Warn("listing obsolete generations", "error", err)
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == generation || !strings.HasPrefix(entry.Name(), "gen-") {
			continue
		}
		directory := filepath.Join(m.settings.IndexPath, entry.Name())
		lease := flock.New(filepath.Join(directory, "LEASE"))
		locked, err := lease.TryLock()
		if err != nil || !locked {
			continue
		}
		removeErr := os.RemoveAll(directory)
		unlockErr := lease.Unlock()
		if err := errors.Join(removeErr, unlockErr); err != nil {
			m.logger.Warn("removing obsolete generation", "error", err)
		}
	}
	return nil
}
