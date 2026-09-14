package kwb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
)

// sourceSelection records build inputs so a later check uses the same policy.
// The project ignore file is always read live, like nested .gitignore files.
type sourceSelection struct {
	MaxFileSize     int      `json:"max_file_size"`
	ExtraExtensions []string `json:"include_ext,omitempty"`
	ExcludeDirs     []string `json:"exclude_dirs,omitempty"`
	ExcludeFiles    []string `json:"exclude_files,omitempty"`
}

func selectionFrom(s *Settings) *sourceSelection {
	return &sourceSelection{s.MaxFileSize, slices.Clone(s.ExtraExtensions),
		slices.Clone(s.ExcludeDirs), slices.Clone(s.ExcludeFiles)}
}

func (s *sourceSelection) equal(other *sourceSelection) bool {
	return s != nil && other != nil && s.MaxFileSize == other.MaxFileSize &&
		slices.Equal(s.ExtraExtensions, other.ExtraExtensions) &&
		slices.Equal(s.ExcludeDirs, other.ExcludeDirs) && slices.Equal(s.ExcludeFiles, other.ExcludeFiles)
}

type FileChanges struct {
	Count int      `json:"count"`
	Paths []string `json:"paths"`
}

// FreshnessResult describes one bounded, read-only scan against a generation.
// Complete refers to scanning, while Truncated refers only to the path samples.
type FreshnessResult struct {
	Status       string           `json:"status"`
	Root         string           `json:"root"`
	Generation   string           `json:"generation,omitempty"`
	Complete     bool             `json:"complete"`
	Truncated    bool             `json:"truncated"`
	CheckedFiles int              `json:"checked_files"`
	New          FileChanges      `json:"new"`
	Changed      FileChanges      `json:"changed"`
	Deleted      FileChanges      `json:"deleted"`
	Excluded     FileChanges      `json:"excluded"`
	Selection    *sourceSelection `json:"selection,omitempty"`
	Diagnostics  []Diagnostic     `json:"diagnostics"`
	detailBytes  int
}

func (r *FreshnessResult) record(changes *FileChanges, path string) {
	changes.Count++
	if len(changes.Paths) >= 100 || r.detailBytes+len(path) > 16*1024 {
		r.Truncated = true
		return
	}
	changes.Paths = append(changes.Paths, path)
	r.detailBytes += len(path)
}

func (s *Service) CheckFreshness(ctx context.Context) (*FreshnessResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()
	return s.index.CheckFreshness(ctx)
}

func (m *indexManager) CheckFreshness(ctx context.Context) (*FreshnessResult, error) {
	result := &FreshnessResult{
		Status: "incomplete", Root: m.settings.RootPath, Diagnostics: []Diagnostic{},
		New: FileChanges{Paths: []string{}}, Changed: FileChanges{Paths: []string{}},
		Deleted: FileChanges{Paths: []string{}}, Excluded: FileChanges{Paths: []string{}},
	}
	err := m.withSnapshot(ctx, func(current *snapshot) error {
		result.Root, result.Generation = current.manifest.Root, current.generation
		settings := *m.settings
		policy := current.manifest.Selection
		if policy == nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "selection_unknown",
				Message: "This index predates recorded build settings; run go42x kwb build before checking freshness"})
			return nil
		}
		settings.MaxFileSize, settings.ExtraExtensions = policy.MaxFileSize, policy.ExtraExtensions
		settings.ExcludeDirs, settings.ExcludeFiles = policy.ExcludeDirs, policy.ExcludeFiles
		if err := settings.Validate(); err != nil {
			return fmt.Errorf("invalid recorded build settings: %w", err)
		}
		result.Selection = policy
		walker := newIndexManager(&settings, m.logger)
		seen := map[string]bool{}
		if err := walker.walkFiles(ctx, result.Root, func(path string, _ os.FileInfo, data []byte) error {
			seen[path] = true
			result.CheckedFiles++
			previous, exists := current.manifest.Files[path]
			if !exists {
				result.record(&result.New, path)
			} else if Hash(data) != previous.Hash {
				result.record(&result.Changed, path)
			}
			return ctx.Err()
		}); err != nil {
			return err
		}
		root, err := os.OpenRoot(result.Root)
		if err != nil {
			return err
		}
		defer root.Close() //nolint:errcheck
		missing := []string{}
		for path := range current.manifest.Files {
			if !seen[path] {
				missing = append(missing, path)
			}
		}
		slices.Sort(missing)
		for _, path := range missing {
			if err := ctx.Err(); err != nil {
				return err
			}
			_, err := root.Lstat(path)
			switch {
			case os.IsNotExist(err):
				result.record(&result.Deleted, path)
			case err != nil:
				return err
			default:
				result.record(&result.Excluded, path)
			}
		}
		generation, err := m.generation()
		if err != nil {
			return err
		}
		if generation != current.generation {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "index_changed",
				Message: "Index changed during the scan; retry against the new generation"})
			return nil
		}
		result.Complete, result.Status = true, "fresh"
		if result.New.Count+result.Changed.Count+result.Deleted.Count+result.Excluded.Count > 0 {
			result.Status = "stale"
		}
		return nil
	})
	if errors.Is(err, ErrRootMismatch) {
		return nil, err
	}
	if err != nil {
		if result.Generation == "" {
			result.Status = "unavailable"
			if errors.Is(err, os.ErrNotExist) {
				result.Status = "missing"
			}
		}
		// Avoid unbounded filesystem error messages in machine-readable output.
		message := err.Error()
		if len(message) > 1024 {
			message = message[:1024]
		}
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "check_incomplete", Message: message})
	}
	return result, errors.Join(err, result.bound())
}

func (r *FreshnessResult) bound() error {
	for {
		data, err := json.Marshal(r)
		if err != nil || len(data) <= MaxResponseBytes {
			return err
		}
		r.Truncated = true
		largest := &r.New
		for _, changes := range []*FileChanges{&r.Changed, &r.Deleted, &r.Excluded} {
			if len(changes.Paths) > len(largest.Paths) {
				largest = changes
			}
		}
		switch {
		case len(largest.Paths) > 0:
			largest.Paths = largest.Paths[:len(largest.Paths)-1]
		case r.Selection != nil:
			r.Selection = nil
			r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: "selection_detail_limit",
				Message: "Recorded build settings omitted to keep the response bounded"})
		default:
			return fmt.Errorf("freshness metadata exceeds response limit")
		}
	}
}
