package kwb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

const (
	priorityBootstrap = iota
	priorityGuidance
	priorityExplicit
	priorityLinked
	priorityRelated
)

const maxContextPaths = 16

type candidate struct {
	doc                  *Document
	path, title, reason  string
	content, hash        string
	start, end, priority int
	evidence             []Evidence
}

type contextBuilder struct {
	service    *Service
	catalog    *Catalog
	result     *ContextResult
	candidates map[string]candidate
}

// Context assembles live guidance, documentation impact, and explicitly supplied
// files. Source discovery belongs to Search and language-server navigation.
func (s *Service) Context(ctx context.Context, options ContextOptions) (*ContextResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()
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
	catalog, err := s.catalog.Load(ctx, projectRoot)
	if err != nil {
		return nil, err
	}
	b := &contextBuilder{
		service: s, catalog: catalog,
		candidates: map[string]candidate{},
		result: &ContextResult{
			Root:        projectRoot,
			Items:       []ContextItem{},
			Diagnostics: slices.Clone(catalog.Diagnostics),
			Coverage:    "Live project documentation and explicitly supplied files; directories select documentation impact only, with no implicit source search",
		},
	}
	if err := b.documentation(ctx, paths); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close() //nolint:errcheck
	if err := b.explicitSources(ctx, root, paths); err != nil {
		return nil, err
	}
	return b.render(ctx, options.MaxBytes)
}

func (b *contextBuilder) diagnostic(code, path, message string) {
	for _, d := range b.result.Diagnostics {
		if d.Code == code && d.Path == path {
			return
		}
	}
	b.result.Diagnostics = append(b.result.Diagnostics, Diagnostic{code, path, message})
}

func (b *contextBuilder) add(c candidate) {
	if previous, ok := b.candidates[c.path]; ok {
		evidence := append(slices.Clone(previous.evidence), c.evidence...)
		if compareCandidates(previous, c) < 0 {
			c = previous
		}
		c.evidence = evidence
	}
	b.candidates[c.path] = c
}

func compareCandidates(a, b candidate) int {
	if a.priority != b.priority {
		return a.priority - b.priority
	}
	if a.path != b.path {
		return strings.Compare(a.path, b.path)
	}
	return a.start - b.start
}

func documentCandidate(doc *Document, reason string, priority int) candidate {
	return candidate{
		doc: doc, path: doc.Path, title: doc.Title, reason: reason, priority: priority,
		content: doc.Content, hash: doc.Hash, start: doc.BodyStartLine, end: doc.TotalLines,
	}
}

func (b *contextBuilder) documentation(ctx context.Context, paths []string) error {
	if doc := b.catalog.ByPath(b.catalog.entrypoint); doc != nil {
		b.add(documentCandidate(doc, "bootstrap", priorityBootstrap))
	}
	for _, id := range b.service.settings.ContextDocs {
		doc, err := b.catalog.ByID(id)
		if err != nil {
			b.diagnostic("guidance_unavailable", id, err.Error())
			continue
		}
		b.add(documentCandidate(doc, "project_guidance", priorityGuidance))
	}
	impact, err := b.catalog.impact(ctx, paths)
	if err != nil {
		return err
	}
	for _, hit := range impact.Documents {
		if hit.Collection == "templates" {
			continue
		}
		priority := priorityLinked
		if hit.Reason == "related_document" {
			priority = priorityRelated
		}
		doc := b.catalog.ByPath(hit.Path)
		if doc == nil {
			return fmt.Errorf("impacted document %q not found in catalog", hit.Path)
		}

		c := documentCandidate(doc, hit.Reason, priority)
		c.evidence = hit.Evidence
		b.add(c)
	}
	if impact.Truncated {
		b.result.Truncated = true
		b.diagnostic("impact_limit", "", "Documentation impact was truncated; narrow the supplied paths")
	}
	return ctx.Err()
}

func (b *contextBuilder) explicitSources(ctx context.Context, root *os.Root, paths []string) error {
	if len(paths) > maxContextPaths {
		b.diagnostic(
			"path_limit",
			"",
			"Explicit reads use the first 16 paths; documentation impact uses all supplied paths",
		)
		paths = paths[:maxContextPaths]
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := root.Stat(path)
		if err != nil {
			b.diagnostic(
				"source_unavailable",
				path,
				"Explicit source is unavailable; documentation impact remains available",
			)
			continue
		}
		if info.IsDir() {
			continue
		}
		if doc := b.catalog.ByPath(path); doc != nil {
			b.add(documentCandidate(doc, "explicit_source", priorityExplicit))
			continue
		}
		data, err := readCandidate(ctx, root, path)
		if err != nil {
			b.diagnostic("source_unavailable", path, err.Error())
			continue
		}
		b.add(candidate{
			path: path, reason: "explicit_source", priority: priorityExplicit,
			content: string(data), hash: Hash(data), start: 1, end: len(sourceLines(string(data))),
		})
	}
	return ctx.Err()
}

func (b *contextBuilder) truncated() {
	b.result.Truncated = true
	b.diagnostic(
		"content_budget",
		"",
		"Additional content was omitted; follow source.next_start_line or read selected files and documents directly",
	)
}

func (b *contextBuilder) render(ctx context.Context, budget int) (*ContextResult, error) {
	ordered := make([]candidate, 0, len(b.candidates))
	guidanceCount := 0
	for _, c := range b.candidates {
		ordered = append(ordered, c)
		if c.priority <= priorityGuidance {
			guidanceCount++
		}
	}
	slices.SortFunc(ordered, compareCandidates)
	guidanceBudget := budget
	if len(ordered) > guidanceCount {
		guidanceBudget = budget / 4
	}
	for _, c := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := budget - b.result.ContentBytes
		if remaining == 0 || len(b.result.Items) == MaxItems {
			b.truncated()
			break
		}
		allocation := remaining
		if c.priority <= priorityGuidance {
			allocation = min(allocation, guidanceBudget/max(1, guidanceCount))
			guidanceCount--
		}
		before := b.result.ContentBytes
		cut := b.renderCandidate(c, allocation)
		if c.priority <= priorityGuidance {
			guidanceBudget -= b.result.ContentBytes - before
		} else if cut {
			break
		}
	}
	return b.bound(ctx)
}

func (b *contextBuilder) renderCandidate(c candidate, budget int) bool {
	if c.end < c.start {
		return false
	}
	source, err := ReadLines(c.content, c.start, 0)
	if err != nil {
		b.diagnostic("source_range_unavailable", c.path, err.Error())
		return false
	}
	source, cut := fitSource(source, budget)
	if cut {
		b.truncated()
	} else if source.NextStartLine != nil {
		b.result.Truncated = true
	}
	if strings.TrimSpace(source.Content) == "" {
		return cut
	}
	item := ContextItem{Path: c.path, Hash: c.hash, Title: c.title,
		Reason: c.reason, Evidence: c.evidence, Source: source}
	if c.doc != nil {
		metadata := c.doc.Metadata
		item.Document, item.StatusMeaning = &metadata, StatusMeaning(metadata)
		item.Replacements, _ = b.catalog.Replacements(c.doc)
	}
	b.result.Items = append(b.result.Items, item)
	b.result.ContentBytes += len(source.Content)
	return cut
}

func (b *contextBuilder) bound(ctx context.Context) (*ContextResult, error) {
	if len(b.result.Diagnostics) > 100 {
		b.result.Diagnostics = b.result.Diagnostics[:100]
		b.result.Truncated = true
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := json.Marshal(b.result)
		if err != nil {
			return nil, err
		}
		if len(data) <= MaxResponseBytes {
			return b.result, nil
		}
		b.result.Truncated = true
		if len(b.result.Diagnostics) > 0 {
			b.result.Diagnostics = b.result.Diagnostics[:len(b.result.Diagnostics)-1]
		} else if len(b.result.Items) > 0 {
			last := b.result.Items[len(b.result.Items)-1]
			b.result.ContentBytes -= len(last.Source.Content)
			b.result.Items = b.result.Items[:len(b.result.Items)-1]
		} else {
			return nil, fmt.Errorf("context response exceeds limit")
		}
	}
}
