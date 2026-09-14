package kwb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

const (
	maxCatalogBytes      = 64 * 1024 * 1024
	maxCatalogFiles      = 10000
	MaxImpactPaths       = 100
	MaxImpactDocuments   = 100
	MaxRelationshipDepth = 2
)

type documentCatalog struct {
	access chan struct{}
	root   string
	cache  map[string]*Document
}

func newDocumentCatalog() *documentCatalog {
	return &documentCatalog{
		access: make(chan struct{}, 1),
		cache:  map[string]*Document{},
	}
}

type Catalog struct {
	Documents   []*Document
	Diagnostics []Diagnostic
	byID        map[string][]*Document
	byPath      map[string]*Document
}

func (c *Catalog) ByID(id string) (*Document, error) {
	matches := c.byID[id]
	if len(matches) == 0 {
		return nil, fmt.Errorf("document ID %q not found", id)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("document ID %q is ambiguous (%d files)", id, len(matches))
	}
	return matches[0], nil
}

func (c *Catalog) ByPath(path string) *Document {
	return c.byPath[path]
}

// Load rechecks live files on every call, caching only Markdown parsing by
// path and content hash. Deleted and renamed files disappear on the next call.
// Markdown with YAML front matter is discovered throughout the checkout,
// respecting .gitignore. The configured entrypoint may be plain Markdown.
// All reads are confined to the project root; symlinks are not catalogued.
func (s *documentCatalog) Load(ctx context.Context, rootPath, entrypoint string) (*Catalog, error) {
	// Waiting for another reader's cache refresh consumes this request's budget.
	select {
	case s.access <- struct{}{}:
		defer func() { <-s.access }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.root != rootPath {
		s.root = rootPath
		s.cache = map[string]*Document{}
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close() //nolint:errcheck
	catalog := &Catalog{
		Documents:   []*Document{},
		Diagnostics: []Diagnostic{},
		byID:        map[string][]*Document{},
		byPath:      map[string]*Document{},
	}
	nextCache := map[string]*Document{}
	total := 0
	files := 0
	err = walkMarkdown(ctx, root, func(path string, entry fs.DirEntry) error {
		if !entry.Type().IsRegular() {
			catalog.Diagnostics = append(
				catalog.Diagnostics,
				Diagnostic{
					"file_skipped",
					path,
					"Only regular authored files are catalogued",
				},
			)
			return nil
		}
		files++
		if files > maxCatalogFiles {
			return fmt.Errorf("documentation discovery exceeds %d Markdown files", maxCatalogFiles)
		}
		file, err := root.Open(path)
		if err != nil {
			return err
		}
		// Keep one extra byte so oversized documents remain parse diagnostics.
		data, readErr := readBounded(ctx, io.LimitReader(file, MaxFileBytes+1), MaxFileBytes+1)
		_ = file.Close()
		if readErr != nil {
			return readErr
		}
		total += len(data)
		if total > maxCatalogBytes {
			return fmt.Errorf("documentation catalog exceeds %d bytes", maxCatalogBytes)
		}
		doc := s.cache[path]
		if doc == nil || doc.Hash != Hash(data) {
			doc, err = Parse(path, data)
			if err != nil {
				catalog.Diagnostics = append(catalog.Diagnostics, Diagnostic{
					"parse_error",
					path,
					err.Error(),
				})
				return nil
			}
		}
		nextCache[path] = doc
		if !doc.hasFrontMatter && path != entrypoint {
			return nil
		}
		catalog.Documents = append(catalog.Documents, doc)
		catalog.byPath[path] = doc
		if doc.ID != "" {
			catalog.byID[doc.ID] = append(catalog.byID[doc.ID], doc)
		}
		if doc.hasFrontMatter {
			catalog.Diagnostics = append(catalog.Diagnostics, doc.validate()...)
		} else {
			catalog.Diagnostics = append(catalog.Diagnostics, doc.Diagnostics...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.cache = nextCache
	if catalog.ByPath(entrypoint) == nil {
		catalog.Diagnostics = append(
			catalog.Diagnostics,
			Diagnostic{
				"docs_entrypoint_missing",
				entrypoint,
				"Documentation entrypoint is missing, excluded, or cannot be parsed",
			},
		)
	}
	positions := map[int]string{}
	for _, doc := range catalog.Documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if doc.ID != "" && len(catalog.byID[doc.ID]) > 1 {
			catalog.Diagnostics = append(
				catalog.Diagnostics,
				Diagnostic{
					"duplicate_id",
					doc.Path,
					"Document ID occurs in multiple files: " + doc.ID,
				},
			)
		}
		if doc.Collection == "handbook" && doc.SidebarPosition > 0 {
			if previous, ok := positions[doc.SidebarPosition]; ok {
				catalog.Diagnostics = append(
					catalog.Diagnostics,
					Diagnostic{
						"duplicate_position",
						doc.Path,
						"Handbook position also used by " + previous,
					},
				)
			}
			positions[doc.SidebarPosition] = doc.Path
		}
		for _, related := range doc.Related {
			target, err := catalog.ByID(related)
			if err != nil || target.Collection == "templates" {
				catalog.Diagnostics = append(
					catalog.Diagnostics,
					Diagnostic{
						"broken_related",
						doc.Path,
						"Related ID is missing, ambiguous, or a template: " + related,
					},
				)
			}
		}
		if doc.SupersededBy != "" {
			_, diagnostics := catalog.Replacements(doc)
			catalog.Diagnostics = append(catalog.Diagnostics, diagnostics...)
		}
		for _, link := range doc.Links {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if _, err := root.Stat(link.Path); err != nil {
				catalog.Diagnostics = append(
					catalog.Diagnostics,
					Diagnostic{
						"broken_link",
						doc.Path,
						fmt.Sprintf("Local target unavailable at line %d: %s", link.Line, link.Path),
					},
				)
			}
		}
	}
	return catalog, ctx.Err()
}

func (c *Catalog) Replacements(doc *Document) ([]Metadata, []Diagnostic) {
	result := []Metadata{}
	diagnostics := []Diagnostic{}
	seen := map[string]bool{doc.ID: true}
	for doc.SupersededBy != "" {
		id := doc.SupersededBy
		if seen[id] || len(result) >= 32 {
			diagnostics = append(
				diagnostics,
				Diagnostic{
					"supersession_cycle",
					doc.Path,
					"Replacement chain cycles or exceeds 32 records",
				},
			)
			break
		}
		seen[id] = true
		replacement, err := c.ByID(id)
		if err != nil || replacement.Collection != "decisions" ||
			!slices.Contains([]string{"accepted", "superseded"}, replacement.Status) {
			diagnostics = append(
				diagnostics,
				Diagnostic{
					"broken_supersession",
					doc.Path,
					"Replacement must be an unambiguous accepted or superseded decision: " + id,
				},
			)
			break
		}
		result = append(result, replacement.Metadata)
		doc = replacement
	}
	return result, diagnostics
}

type DocumentResult struct {
	Document      Document     `json:"document"`
	Source        Source       `json:"source"`
	StatusMeaning string       `json:"status_meaning"`
	Replacements  []Metadata   `json:"replacements"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
	Truncated     bool         `json:"truncated"`
}

// walkMarkdown discovers candidates throughout the checkout. Nested .gitignore
// rules control exclusions; Git internals and symlinked directories are skipped.
func walkMarkdown(ctx context.Context, root *os.Root, visit func(string, fs.DirEntry) error) error {
	patterns := map[string][]gitignore.Pattern{}
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if err != nil {
			return err
		}
		inherited := patterns[path.Dir(name)]
		if name != "." {
			if entry.IsDir() && entry.Name() == ".git" {
				return fs.SkipDir
			}
			if gitignore.NewMatcher(inherited).Match(strings.Split(name, "/"), entry.IsDir()) {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		if entry.IsDir() {
			data, err := fs.ReadFile(root.FS(), path.Join(name, ".gitignore"))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			local := slices.Clone(inherited)
			var domain []string
			if name != "." {
				domain = strings.Split(name, "/")
			}
			for _, line := range strings.Split(string(data), "\n") {
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
				local = append(local, gitignore.ParsePattern(line, domain))
			}
			patterns[name] = local
			return nil
		}
		if ext := strings.ToLower(path.Ext(name)); ext == ".md" || ext == ".markdown" {
			return visit(name, entry)
		}
		return nil
	})
}

type Evidence struct {
	ChangedPath  string   `json:"changed_path"`
	LinkPath     string   `json:"link_path,omitempty"`
	Line         int      `json:"line,omitempty"`
	Relationship []string `json:"relationship,omitempty"`
}

type ImpactDocument struct {
	Metadata
	Path          string     `json:"path"`
	Reason        string     `json:"reason"`
	StatusMeaning string     `json:"status_meaning"`
	Evidence      []Evidence `json:"evidence"`
}

type ImpactResult struct {
	Documents     []ImpactDocument `json:"documents"`
	UnmappedPaths []string         `json:"unmapped_paths"`
	Diagnostics   []Diagnostic     `json:"diagnostics"`
	Truncated     bool             `json:"truncated"`
	Coverage      string           `json:"coverage"`
}

func NormalizePaths(paths []string) ([]string, error) {
	if len(paths) > MaxImpactPaths {
		return nil, fmt.Errorf("at most %d paths are supported", MaxImpactPaths)
	}
	result := []string{}
	total := 0
	for _, p := range paths {
		total += len(p)
		if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || strings.Contains(p, ":") ||
			len(p) > 4096 ||
			total > 16*1024 {
			return nil, fmt.Errorf("paths must be project-relative and total at most 16 KiB")
		}
		p = path.Clean(p)
		if p == ".." || strings.HasPrefix(p, "../") {
			return nil, fmt.Errorf("paths must remain inside the project root")
		}
		result = append(result, p)
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

// PathsOverlap compares whole path segments, accepting deleted files and folders.
func PathsOverlap(a, b string) bool {
	return a == "." || b == "." || a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (c *Catalog) Impact(paths []string) *ImpactResult {
	result, _ := c.impact(context.Background(), paths)
	return result
}

func (c *Catalog) impact(ctx context.Context, paths []string) (*ImpactResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &ImpactResult{
		Documents:     []ImpactDocument{},
		UnmappedPaths: []string{},
		Diagnostics:   slices.Clone(c.Diagnostics),
		Coverage:      "Explicit Markdown links and up to two related/supersession edges; no match does not establish absence of documentation impact",
	}
	hits := map[string]*ImpactDocument{}
	mapped := map[string]bool{}
	for _, doc := range c.Documents {
		for _, changed := range paths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if PathsOverlap(doc.Path, changed) {
				hit := impactHit(hits, doc, "changed_document")
				hit.Evidence = append(hit.Evidence, Evidence{
					ChangedPath: changed,
				})
				mapped[changed] = true
			}
			for _, link := range doc.Links {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if PathsOverlap(link.Path, changed) {
					hit := impactHit(hits, doc, "direct_link")
					if len(hit.Evidence) < 10 {
						hit.Evidence = append(
							hit.Evidence,
							Evidence{
								ChangedPath: changed,
								LinkPath:    link.Path,
								Line:        link.Line,
							},
						)
					}
					mapped[changed] = true
				}
			}
		}
	}
	// Undirected authored relationships allow a requirement that points at a
	// handbook page to be discovered when the page's implementation changes.
	edges := map[string][]string{}
	for _, doc := range c.Documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ids := slices.Clone(doc.Related)
		if doc.SupersededBy != "" {
			ids = append(ids, doc.SupersededBy)
		}
		for _, id := range ids {
			target, err := c.ByID(id)
			if err != nil {
				continue
			}
			edges[doc.Path] = append(edges[doc.Path], target.Path)
			edges[target.Path] = append(edges[target.Path], doc.Path)
		}
	}
	for key := range edges {
		slices.Sort(edges[key])
		edges[key] = slices.Compact(edges[key])
	}
	type node struct {
		path    string
		depth   int
		trail   []string
		changed string
	}
	queue := []node{}
	seen := map[string]bool{}
	for _, doc := range c.Documents {
		if hit := hits[doc.Path]; hit != nil {
			queue = append(queue, node{
				doc.Path,
				0,
				[]string{doc.ID},
				hit.Evidence[0].ChangedPath,
			})
			seen[doc.Path] = true
		}
	}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current := queue[0]
		queue = queue[1:]
		if current.depth >= MaxRelationshipDepth {
			continue
		}
		for _, next := range edges[current.path] {
			if seen[next] {
				continue
			}
			seen[next] = true
			doc := c.byPath[next]
			trail := append(slices.Clone(current.trail), doc.ID)
			hit := impactHit(hits, doc, "related_document")
			hit.Evidence = append(hit.Evidence, Evidence{
				ChangedPath:  current.changed,
				Relationship: trail,
			})
			queue = append(queue, node{
				next,
				current.depth + 1,
				trail,
				current.changed,
			})
		}
	}
	for _, doc := range c.Documents {
		if hit := hits[doc.Path]; hit != nil {
			result.Documents = append(result.Documents, *hit)
		}
	}
	slices.SortStableFunc(result.Documents, func(a, b ImpactDocument) int {
		rank := func(reason string) int {
			switch reason {
			case "changed_document":
				return 0
			case "direct_link":
				return 1
			default:
				return 2
			}
		}
		if difference := rank(a.Reason) - rank(b.Reason); difference != 0 {
			return difference
		}
		return strings.Compare(a.Path, b.Path)
	})
	for _, p := range paths {
		if !mapped[p] {
			result.UnmappedPaths = append(result.UnmappedPaths, p)
		}
	}
	if len(result.Documents) > MaxImpactDocuments {
		result.Documents = result.Documents[:MaxImpactDocuments]
		result.Truncated = true
	}
	if len(result.Diagnostics) > 100 {
		result.Diagnostics = result.Diagnostics[:100]
		result.Truncated = true
	}
	return result, ctx.Err()
}

func impactHit(hits map[string]*ImpactDocument, doc *Document, reason string) *ImpactDocument {
	if hit := hits[doc.Path]; hit != nil {
		return hit
	}
	hit := &ImpactDocument{
		Metadata:      doc.Metadata,
		Path:          doc.Path,
		Reason:        reason,
		StatusMeaning: StatusMeaning(doc.Metadata),
		Evidence:      []Evidence{},
	}
	hits[doc.Path] = hit
	return hit
}

func boundImpact(result *ImpactResult) error {
	for {
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if len(data) <= MaxResponseBytes {
			return nil
		}
		result.Truncated = true
		if len(result.Diagnostics) > 0 {
			result.Diagnostics = result.Diagnostics[:len(result.Diagnostics)-1]
		} else if len(result.Documents) > 0 {
			result.Documents = result.Documents[:len(result.Documents)-1]
		} else {
			return fmt.Errorf("impact response exceeds limit")
		}
	}
}
