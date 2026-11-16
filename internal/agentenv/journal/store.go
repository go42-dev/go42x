package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/go42-dev/go42x/internal/config"
)

// lockTimeout bounds the wait for another process that is changing the journal.
const lockTimeout = 10 * time.Second

const (
	// trashPrefix marks an entry directory that gc took out of the journal to delete it.
	trashPrefix = ".trash-"
	// tempInfix marks the temporary files of writeFileAtomic.
	tempInfix = ".tmp-"
)

// buildSubdirs are owned by tasks and tools; other content of the build directory is stray.
var buildSubdirs = []string{
	config.DefaultBuildBinDir,
	config.DefaultBuildDistDir,
	config.DefaultBuildReportsDir,
	config.DefaultBuildCacheDir,
	config.DefaultBuildTmpDir,
}

// Problem describes an entry directory whose record cannot be used.
type Problem struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// StrayItem is journal or build directory content that no entry describes.
type StrayItem struct {
	Path       string    `json:"path"`
	Directory  bool      `json:"directory"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

// IndexEntry summarizes an entry in .index.json.
type IndexEntry struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Status    Status     `json:"status"`
	Tags      []string   `json:"tags"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	Runtime   string     `json:"runtime"`
}

type indexDocument struct {
	Version int          `json:"version"`
	Entries []IndexEntry `json:"entries"`
}

// store reads and writes the journal of one project.
type store struct {
	root string
	dir  string
}

// snapshot is the content of the journal directory at one point.
type snapshot struct {
	entries []*Entry
	invalid []Problem
	stray   []StrayItem
}

func newStore(root string) store {
	return store{
		root: root,
		dir:  filepath.Join(root, filepath.FromSlash(config.JournalDir)),
	}
}

func (s store) entryDir(id string) string {
	return filepath.Join(s.dir, id)
}

// relative returns a project-relative path with forward slashes.
func (s store) relative(path string) string {
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// lock serializes changes to the journal across processes. The returned function
// releases the lock.
func (s store) lock(ctx context.Context) (func() error, error) {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return nil, fmt.Errorf("create journal directory: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()

	lock := flock.New(filepath.Join(s.dir, lockFile))
	locked, err := lock.TryLockContext(ctx, 20*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("lock journal: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("journal lock unavailable")
	}
	return lock.Unlock, nil
}

// read loads and validates one entry.
func (s store) read(id string) (*Entry, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filepath.Join(s.entryDir(id), entryFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("journal entry %s does not exist", id)
	}
	if err != nil {
		return nil, err
	}
	return decodeEntry(data, id)
}

// write stores an entry record atomically.
func (s store) write(entry *Entry) error {
	data, err := encodeEntry(entry)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.entryDir(entry.ID), entryFile), data)
}

// appendEvent adds one line to the entry's log. When an interrupted write left the last
// line without its newline, the event starts on a new line, so only the damaged line is lost.
func (s store) appendEvent(id string, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	path := filepath.Join(s.entryDir(id), logFile)
	torn, err := tornTail(path)
	if err != nil {
		return err
	}
	if torn {
		data = append([]byte{'\n'}, data...)
	}

	// #nosec G304 -- The log path is fixed inside a validated entry directory.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	return errors.Join(writeErr, file.Close())
}

// tornTail reports whether the file at path ends without a newline.
func tornTail(path string) (bool, error) {
	// #nosec G304 -- The caller passes the log path inside a validated entry directory.
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close() //nolint:errcheck

	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// remove deletes an entry directory. It first moves the directory to a hidden trash name,
// so a failed deletion leaves no partial entry; the remains are listed as stray content.
func (s store) remove(id string) error {
	trash := filepath.Join(s.dir, trashPrefix+id)
	if err := os.Rename(s.entryDir(id), trash); err != nil {
		return fmt.Errorf("remove journal entry %s: %w", id, err)
	}
	if err := os.RemoveAll(trash); err != nil {
		return fmt.Errorf("remove journal entry %s; delete the remains in %s: %w", id, s.relative(trash), err)
	}
	return nil
}

// events reads the entry's log. Lines that do not parse are skipped, so a damaged
// line does not hide the rest of the history.
func (s store) events(id string) ([]Event, error) {
	data, err := os.ReadFile(filepath.Join(s.entryDir(id), logFile))
	if errors.Is(err, fs.ErrNotExist) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, err
	}

	events := []Event{}
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		var event Event
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &event) != nil {
			continue
		}

		events = append(events, event)
	}
	return events, nil
}

// scan reads every entry, reporting unusable records and content without a record.
// Stray content includes temporary files that an interrupted write left behind and
// the remains of a failed gc deletion.
func (s store) scan(ctx context.Context) (*snapshot, error) {
	result := &snapshot{
		entries: []*Entry{},
		invalid: []Problem{},
		stray:   []StrayItem{},
	}

	children, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}

	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(s.dir, child.Name())
		switch {
		case leftover(child.Name()):
			result.stray = append(result.stray, strayItem(path, s.relative(path)))
			continue
		case strings.HasPrefix(child.Name(), "."):
			// Other hidden names hold the index and the lock.
			continue
		case !child.IsDir():
			result.stray = append(result.stray, strayItem(path, s.relative(path)))
			continue
		}

		// #nosec G304 -- The record has a fixed name inside a directory listed from the journal.
		data, err := os.ReadFile(filepath.Join(path, entryFile))
		if errors.Is(err, fs.ErrNotExist) {
			result.stray = append(result.stray, strayItem(path, s.relative(path)))
			continue
		}
		if err == nil {
			var entry *Entry
			entry, err = decodeEntry(data, child.Name())
			if err == nil {
				result.entries = append(result.entries, entry)
				result.stray = append(result.stray, s.leftovers(path)...)
				continue
			}
		}

		result.invalid = append(result.invalid, Problem{
			Path:  s.relative(path),
			Error: err.Error(),
		})
	}

	slices.SortFunc(result.entries, func(a, b *Entry) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return result, nil
}

// leftovers lists the temporary files that interrupted writes left in an entry directory.
func (s store) leftovers(dir string) []StrayItem {
	children, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var items []StrayItem
	for _, child := range children {
		if leftover(child.Name()) {
			path := filepath.Join(dir, child.Name())
			items = append(items, strayItem(path, s.relative(path)))
		}
	}
	return items
}

// leftover reports whether name is a temporary file of writeFileAtomic or the remains of a
// gc deletion.
func leftover(name string) bool {
	return strings.HasPrefix(name, trashPrefix) || (strings.HasPrefix(name, ".") && strings.Contains(name, tempInfix))
}

// strayBuild lists top-level build directory content outside the standard subdirectories.
func (s store) strayBuild(ctx context.Context) ([]StrayItem, error) {
	buildDir, err := config.ProjectBuildDir(s.root)
	if err != nil {
		return nil, err
	}
	children, err := os.ReadDir(buildDir)
	if errors.Is(err, fs.ErrNotExist) {
		return []StrayItem{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read build directory: %w", err)
	}

	items := []StrayItem{}
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.HasPrefix(child.Name(), ".") || (child.IsDir() && slices.Contains(buildSubdirs, child.Name())) {
			continue
		}

		path := filepath.Join(buildDir, child.Name())
		items = append(items, strayItem(path, s.relative(path)))
	}
	return items, nil
}

// renderIndex returns the .index.json content for the supplied entries.
func renderIndex(entries []*Entry) ([]byte, error) {
	document := indexDocument{
		Version: entryVersion,
		Entries: make([]IndexEntry, 0, len(entries)),
	}
	for _, entry := range entries {
		document.Entries = append(document.Entries, IndexEntry{
			ID:        entry.ID,
			Title:     entry.Title,
			Status:    entry.Status,
			Tags:      entry.Tags,
			CreatedAt: entry.CreatedAt,
			UpdatedAt: entry.UpdatedAt,
			ExpiresAt: entry.ExpiresAt,
			Runtime:   entry.Runtime,
		})
	}

	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// writeIndex rebuilds .index.json from the current entries.
func (s store) writeIndex(ctx context.Context) (int, error) {
	current, err := s.scan(ctx)
	if err != nil {
		return 0, err
	}

	data, err := renderIndex(current.entries)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return 0, err
	}
	return len(current.entries), writeFileAtomic(filepath.Join(s.dir, indexFile), data)
}

// entryFiles lists regular files in an entry directory, relative to the project root.
func (s store) entryFiles(id string) ([]EntryFile, error) {
	dir := s.entryDir(id)
	files := []EntryFile{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		files = append(files, EntryFile{
			Path: s.relative(path),
			Size: info.Size(),
		})
		return nil
	})
	return files, err
}

func strayItem(path, relative string) StrayItem {
	item := StrayItem{Path: relative}
	info, err := os.Lstat(path)
	if err != nil {
		return item
	}

	item.Directory = info.IsDir()
	item.ModifiedAt = info.ModTime().UTC()
	item.Size = treeSize(path)
	return item
}

// treeSize sums the sizes of regular files below path without following links.
func treeSize(path string) int64 {
	var size int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
}

// writeFileAtomic replaces path so readers see either the old or the new content.
func writeFileAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+tempInfix+"*")
	if err != nil {
		return err
	}

	name := temp.Name()
	_, writeErr := temp.Write(data)
	if err := errors.Join(writeErr, temp.Sync(), temp.Close()); err != nil {
		return errors.Join(err, os.Remove(name))
	}
	if err := os.Rename(name, path); err != nil {
		return errors.Join(err, os.Remove(name))
	}
	return nil
}
