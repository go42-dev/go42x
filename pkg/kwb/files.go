package kwb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

var defaultExcludedDirs = []string{
	".git", "vendor", "node_modules", ".idea", ".vscode", "dist", "build", "bin", ".go42x",
	".build", ".tools", ".task", ".venv", "__pycache__",
}

var defaultExtensions = map[string]bool{
	".go": true, ".md": true, ".markdown": true, ".txt": true, ".rst": true, ".adoc": true,
	".yaml": true, ".yml": true, ".mod": true, ".proto": true, ".sql": true,
	".json": true, ".toml": true, ".env": true, ".sh": true, ".ini": true,
	".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".py": true, ".rs": true,
	".java": true, ".c": true, ".h": true, ".cpp": true, ".cs": true, ".rb": true, ".php": true,
}

var defaultExcludedFiles = []string{
	"go.sum",
	"package-lock.json",
	"yarn.lock",
	"pnpm-lock.yaml",
	"Cargo.lock",
	"poetry.lock",
}

var defaultFilenames = []string{"Makefile", "Dockerfile", ".gitignore", ".env"}

func (m *indexManager) shouldIndexFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, extra := range m.settings.ExtraExtensions {
		if name == extra || strings.EqualFold(ext, "."+strings.TrimPrefix(extra, ".")) {
			return true
		}
	}
	if slices.Contains(defaultExcludedFiles, name) {
		return false
	}
	return slices.Contains(defaultFilenames, name) || defaultExtensions[ext]
}

// walkFiles applies nested .gitignore rules even outside Git repositories.
// Ignored directories are pruned, matching Git's directory-negation semantics.
func (m *indexManager) walkFiles(
	ctx context.Context,
	root string,
	visit func(string, os.FileInfo, []byte) error,
) error {
	return m.walkDirectory(ctx, root, "", nil, visit)
}

func (m *indexManager) walkDirectory(ctx context.Context, root, relative string, inherited []gitignore.Pattern,
	visit func(string, os.FileInfo, []byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Join(root, relative)
	patterns := slices.Clone(inherited)
	// #nosec G304 -- Indexing reads .gitignore files from the caller-selected local project tree.
	ignoreData, err := os.ReadFile(filepath.Join(directory, ".gitignore"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var domain []string
	if relative != "" {
		domain = strings.Split(filepath.ToSlash(relative), "/")
	}
	for _, line := range strings.Split(string(ignoreData), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Preserve descendant-only matching when pruning directories.
		if strings.HasSuffix(line, "/**") {
			line += "/*"
		}
		if line == "**" {
			line = "*"
		}
		if line == "!**" {
			line = "!*"
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	matcher := gitignore.NewMatcher(patterns)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := filepath.Join(relative, entry.Name())
		path := filepath.Join(root, rel)
		if path == m.settings.IndexPath {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if matcher.Match(strings.Split(filepath.ToSlash(rel), "/"), entry.IsDir()) {
			continue
		}
		if entry.IsDir() {
			if slices.Contains(defaultExcludedDirs, entry.Name()) ||
				slices.Contains(m.settings.ExcludeDirs, entry.Name()) ||
				slices.Contains(m.settings.ExcludeDirs, filepath.ToSlash(rel)) {
				continue
			}
			if err := m.walkDirectory(ctx, root, rel, patterns, visit); err != nil {
				return err
			}
			continue
		}
		if !m.shouldIndexFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > int64(m.settings.MaxFileSize) {
			continue
		}
		data, err := readSource(ctx, path, m.settings.MaxFileSize)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			continue
		}
		if filepath.Ext(path) == ".go" && isGeneratedGo(data) {
			continue
		}
		if err := visit(filepath.ToSlash(rel), info, data); err != nil {
			return err
		}
	}
	return nil
}

func isGeneratedGo(data []byte) bool {
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("// Code generated ")) &&
			bytes.HasSuffix(bytes.TrimSpace(line), []byte(" DO NOT EDIT.")) {
			return true
		}
		if bytes.HasPrefix(line, []byte("package ")) {
			break
		}
	}
	return false
}

func readSource(ctx context.Context, path string, limit int) ([]byte, error) {
	// #nosec G304 -- Only the local indexing walk calls this; remote file requests use root-scoped reads.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readBounded(ctx, file, limit)
}

func readBounded(ctx context.Context, reader io.Reader, limit int) ([]byte, error) {
	var data bytes.Buffer
	buffer := make([]byte, 32*1024)
	reader = io.LimitReader(reader, int64(limit)+1)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := reader.Read(buffer)
		data.Write(buffer[:n])
		if data.Len() > limit {
			return nil, fmt.Errorf("file exceeds maximum size of %d bytes", limit)
		}
		if err == io.EOF {
			return data.Bytes(), ctx.Err()
		}
		if err != nil {
			return nil, err
		}
	}
}

type Source struct {
	Content       string `json:"content"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	TotalLines    int    `json:"total_lines"`
	NextStartLine *int   `json:"next_start_line,omitempty"`
}

func ReadLines(content string, start, end int) (Source, error) {
	result := Source{}
	if start < 0 || end < 0 {
		return result, fmt.Errorf("line numbers cannot be negative")
	}
	if start == 0 {
		start = 1
	}
	if end == 0 {
		end = start + 199
	}
	if end < start || end-start >= MaxReadLines {
		return result, fmt.Errorf("request between 1 and %d lines", MaxReadLines)
	}
	if content == "" {
		return result, nil
	}
	lines := sourceLines(content)
	result.TotalLines = len(lines)
	if start > len(lines) {
		return result, fmt.Errorf("start_line exceeds file length of %d lines", len(lines))
	}
	end = min(end, len(lines))
	result.StartLine = start
	result.EndLine = start - 1
	var out strings.Builder
	for i := start - 1; i < end; i++ {
		if out.Len()+len(lines[i])+1 > MaxReadBytes {
			if i == start-1 {
				return result, fmt.Errorf("line %d exceeds the %d-byte response limit", start, MaxReadBytes)
			}
			break
		}
		out.WriteString(lines[i])
		out.WriteByte('\n')
		result.EndLine = i + 1
	}
	result.Content = out.String()
	if result.EndLine < len(lines) {
		next := result.EndLine + 1
		result.NextStartLine = &next
	}
	return result, nil
}
