package kwb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"
)

const (
	priorityBootstrap = iota
	priorityGuidance
	priorityExact
	priorityScoped
	priorityLinked
	priorityDocument
	prioritySearch
)

const (
	maxContextQueries = 48
	maxContextPaths   = 16
	maxContextRanges  = 4
)

type candidate struct {
	doc                       *Document
	path, title, reason, hash string
	query                     string
	start, end, priority      int
	score                     float64
	evidence                  []Evidence
}

type candidateKey struct {
	path       string
	start, end int
}

type contextBuilder struct {
	service     *Service
	catalog     *Catalog
	result      *ContextResult
	terms       []string
	identifiers []string
	candidates  map[candidateKey]candidate
}

func (s *Service) Context(ctx context.Context, options ContextOptions) (*ContextResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()
	if strings.TrimSpace(options.Task) == "" || len(options.Task) > 4096 {
		return nil, fmt.Errorf("task must contain 1 to 4096 bytes")
	}
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
	catalog, err := s.catalog.Load(ctx, projectRoot, s.settings.Entrypoint)
	if err != nil {
		return nil, err
	}
	b := &contextBuilder{
		service: s, catalog: catalog, terms: keywords(options.Task), identifiers: taskIdentifiers(options.Task),
		candidates: map[candidateKey]candidate{},
		result: &ContextResult{
			Root:        projectRoot,
			Items:       []ContextItem{},
			Diagnostics: slices.Clone(catalog.Diagnostics),
			Coverage:    "Live documentation and scoped source candidates; retrieved source hashes verified, index-wide freshness unchecked",
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
	scopes, err := b.explicitSources(ctx, root, paths)
	if err != nil {
		return nil, err
	}
	if err := b.indexedSources(ctx, scopes); err != nil {
		return nil, err
	}
	return b.render(ctx, root, options.MaxBytes)
}

func taskIdentifiers(task string) []string {
	result := []string{}
	for _, word := range strings.FieldsFunc(task, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.'
	}) {
		word = strings.Trim(word, ".")
		if len(keywords(word)) == 0 || !strings.ContainsAny(word, "._") && word == strings.ToLower(word) {
			continue
		}
		if !slices.Contains(result, word) {
			result = append(result, word)
		}
		if len(result) == 8 {
			break
		}
	}
	return result
}

func (b *contextBuilder) diagnostic(code, path, message string) {
	for _, d := range b.result.Diagnostics {
		if d.Code == code && d.Path == path {
			return
		}
	}
	b.result.Diagnostics = append(b.result.Diagnostics, Diagnostic{Code: code, Path: path, Message: message})
}

func (b *contextBuilder) add(c candidate) {
	key := candidateKey{c.path, c.start, c.end}
	if previous, ok := b.candidates[key]; ok {
		combined := previous.score + c.score
		evidence := append(slices.Clone(previous.evidence), c.evidence...)
		if compareCandidates(previous, c) < 0 {
			c = previous
		}
		c.score = combined
		c.evidence = evidence
	}
	b.candidates[key] = c
}

func compareCandidates(a, b candidate) int {
	if a.priority != b.priority {
		return a.priority - b.priority
	}
	if a.score != b.score {
		if a.score > b.score {
			return -1
		}
		return 1
	}
	if a.path != b.path {
		return strings.Compare(a.path, b.path)
	}
	if a.start != b.start {
		return a.start - b.start
	}
	return a.end - b.end
}

func textRelevance(title, body string, terms []string) float64 {
	title, body = strings.ToLower(title), strings.ToLower(body)
	score := 0.0
	for _, term := range terms {
		if strings.Contains(title, term) {
			score += 5
		}
		if strings.Contains(body, term) {
			score++
		}
	}
	return score
}

// Chunking uses section-local content and retains the heading breadcrumb. A
// parent heading therefore cannot win by matching all of its descendants.
func (b *contextBuilder) documentCandidates(doc *Document, reason string, priority int) []candidate {
	result := []candidate{}
	for _, chunk := range chunkFile(doc.Path, []byte(doc.Content)) {
		result = append(result, candidate{
			doc: doc, path: doc.Path, title: chunk.Title, reason: reason, priority: priority,
			start: chunk.StartLine, end: chunk.EndLine,
			score: textRelevance(chunk.Title, chunk.Content, b.terms),
		})
	}
	slices.SortFunc(result, compareCandidates)
	if len(result) > 0 && result[0].score == 0 {
		return result[:1]
	}
	result = slices.DeleteFunc(result, func(c candidate) bool { return c.score == 0 })
	return result[:min(len(result), maxContextRanges)]
}

func (b *contextBuilder) documentation(ctx context.Context, paths []string) error {
	if doc := b.catalog.ByPath(b.service.settings.Entrypoint); doc != nil {
		chunks := chunkFile(doc.Path, []byte(doc.Content))
		if len(chunks) > 0 {
			b.add(candidate{doc: doc, path: doc.Path, title: doc.Title, reason: "bootstrap",
				priority: priorityBootstrap, start: chunks[0].StartLine, end: chunks[0].EndLine})
		}
	}
	for _, id := range b.service.settings.ContextDocs {
		doc, err := b.catalog.ByID(id)
		if err != nil {
			b.diagnostic("guidance_unavailable", id, err.Error())
			continue
		}
		for _, c := range b.documentCandidates(doc, "project_guidance", priorityGuidance) {
			b.add(c)
		}
	}
	impact, err := b.catalog.impact(ctx, paths)
	if err != nil {
		return err
	}
	for _, hit := range impact.Documents {
		if doc := b.catalog.ByPath(hit.Path); doc != nil {
			for _, c := range b.documentCandidates(doc, hit.Reason, priorityLinked) {
				c.evidence = hit.Evidence
				b.add(c)
			}
		}
	}
	ranked := []candidate{}
	for _, doc := range b.catalog.Documents {
		if err := ctx.Err(); err != nil {
			return err
		}
		if doc.Collection == "templates" && !isAuthoring(b.terms) {
			continue
		}
		for _, c := range b.documentCandidates(doc, "task_keywords", priorityDocument) {
			if c.score > 0 {
				ranked = append(ranked, c)
			}
		}
	}
	slices.SortFunc(ranked, compareCandidates)
	for _, c := range ranked[:min(len(ranked), 12)] {
		b.add(c)
	}
	return ctx.Err()
}

func (b *contextBuilder) explicitSources(ctx context.Context, root *os.Root, paths []string) ([]string, error) {
	scopes := []string{}
	if len(paths) > maxContextPaths {
		b.diagnostic(
			"path_limit",
			"",
			"Source retrieval uses the first 16 paths; narrow paths for additional source coverage",
		)
		paths = paths[:maxContextPaths]
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
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
			scopes = append(scopes, strings.TrimSuffix(path, "/")+"/")
			continue
		}
		if doc := b.catalog.ByPath(path); doc != nil {
			for _, c := range b.documentCandidates(doc, "explicit_source", priorityScoped) {
				b.add(c)
			}
			continue
		}
		data, err := readCandidate(ctx, root, path)
		if err != nil {
			b.diagnostic("source_unavailable", path, err.Error())
			continue
		}
		ranked := []candidate{}
		hash := Hash(data)
		for _, chunk := range chunkFile(path, data) {
			c := candidate{path: path, hash: hash, title: chunk.Title,
				reason: "explicit_source", priority: priorityScoped, start: chunk.StartLine, end: chunk.EndLine,
				score: textRelevance(chunk.Title, chunk.Content, b.terms)}
			for _, id := range b.identifiers {
				if slices.Contains(chunk.Symbols, id) {
					c.priority, c.reason, c.query = priorityExact, "exact_symbol", id
				}
			}
			ranked = append(ranked, c)
		}
		slices.SortFunc(ranked, compareCandidates)
		for i, c := range ranked[:min(len(ranked), maxContextRanges)] {
			if i == 0 || c.score > 0 || c.priority == priorityExact {
				b.add(c)
			}
		}
	}
	return scopes, ctx.Err()
}

func (b *contextBuilder) indexedSources(ctx context.Context, scopes []string) error {
	stats, err := b.service.GetStats(ctx)
	if errors.Is(err, ErrRootMismatch) {
		return err
	}
	if err != nil {
		b.diagnostic(
			"index_unavailable",
			"",
			"Index missing, unreadable, or incompatible; rebuild with go42x kwb build --rebuild",
		)
		b.result.Coverage = "Live documentation and explicitly supplied files only; indexed source coverage is reduced"
		return ctx.Err()
	}
	if stats.RootPath != b.result.Root {
		return fmt.Errorf("%w: project root changed during context retrieval", ErrRootMismatch)
	}
	queries := slices.Clone(b.identifiers)
	if len(b.terms) > 0 {
		queries = append(queries, strings.Join(b.terms, " "))
	}
	queries = append(queries, b.terms[:min(len(b.terms), 6)]...)
	queries = uniqueStrings(queries)
	// Always leave room for global supporting evidence after scoped queries.
	requests := 0
	for _, scope := range append(scopes, "") {
		for _, query := range queries {
			if scope != "" && requests >= maxContextQueries-len(queries) {
				b.diagnostic("query_limit", "", "Scoped search budget reached; narrow paths for additional coverage")
				break
			}
			requests++
			response, err := b.service.Search(ctx, SearchOptions{Query: query, PathPrefix: scope, Limit: 10})
			if err != nil {
				b.diagnostic("search_failed", "", "A keyword search failed; source coverage may be reduced")
				return ctx.Err()
			}
			if response.Generation != stats.Generation {
				b.diagnostic(
					"index_changed",
					"",
					"Index changed during retrieval; returned candidates are verified against current source",
				)
			}
			for rank, hit := range response.Results {
				b.indexHit(hit, query, scope, rank)
			}
		}
	}
	return ctx.Err()
}

func uniqueStrings(values []string) []string {
	result := []string{}
	for _, value := range values {
		if !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}

func (b *contextBuilder) indexHit(hit SearchResult, query, scope string, rank int) {
	c := candidate{path: hit.Path, hash: hit.SourceHash, title: hit.Title, query: query,
		reason: "index_search", priority: prioritySearch, start: hit.ChunkStartLine, end: hit.ChunkEndLine,
		score: 1 / float64(60+rank+1)}
	if c.start == 0 || c.end == 0 {
		c.start, c.end = hit.StartLine, hit.EndLine
	}
	if scope != "" {
		c.priority, c.reason = priorityScoped, "scoped_search"
	}
	if slices.Contains(b.identifiers, query) && slices.Contains(hit.Symbols, query) {
		c.priority, c.reason = priorityExact, "exact_symbol"
	}
	if doc := b.catalog.ByPath(hit.Path); doc != nil {
		if doc.Collection == "templates" && !isAuthoring(b.terms) {
			return
		}
		if doc.Hash != hit.SourceHash {
			b.diagnostic(
				"stale_candidate",
				hit.Path,
				"Indexed document differs from current source; selecting live sections",
			)
			for _, current := range b.documentCandidates(doc, "task_keywords", priorityDocument) {
				b.add(current)
			}
			return
		}
		c.doc = doc
	}
	b.add(c)
}

func (b *contextBuilder) render(ctx context.Context, root *os.Root, budget int) (*ContextResult, error) {
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
			b.diagnostic(
				"content_budget",
				"",
				"Additional ranked source ranges were omitted; narrow the task or increase max_bytes",
			)
			b.result.Truncated = true
			break
		}
		allocation := remaining
		if c.priority <= priorityGuidance {
			allocation = min(allocation, guidanceBudget/max(1, guidanceCount))
			guidanceCount--
		}
		before := b.result.ContentBytes
		b.renderCandidate(ctx, root, c, allocation)
		if c.priority <= priorityGuidance {
			guidanceBudget -= b.result.ContentBytes - before
		}
	}
	return b.bound(ctx)
}

func (b *contextBuilder) renderCandidate(ctx context.Context, root *os.Root, c candidate, budget int) {
	content, hash := "", c.hash
	if c.doc != nil {
		content, hash = c.doc.Content, c.doc.Hash
	} else {
		data, err := readCandidate(ctx, root, c.path)
		if err != nil {
			b.diagnostic("source_unavailable", c.path, "Retrieved source could not be read")
			return
		}
		if Hash(data) != hash {
			b.diagnostic("stale_candidate", c.path, "Source changed; stale range omitted, rebuild with go42x kwb build")
			return
		}
		content = string(data)
	}
	for _, span := range uncoveredRanges(c, hash, b.result.Items) {
		if len(b.result.Items) >= MaxItems {
			b.result.Truncated = true
			return
		}
		source, err := ReadLines(content, span.start, span.end)
		if err != nil {
			b.diagnostic("source_range_unavailable", c.path, err.Error())
			continue
		}
		source, cut := fitSource(source, budget)
		if cut || source.NextStartLine != nil {
			b.result.Truncated = true
		}
		if cut {
			b.diagnostic(
				"content_budget",
				c.path,
				"A selected range was shortened by the content budget; follow source.next_start_line",
			)
		}
		if source.Content == "" {
			continue
		}
		item := ContextItem{Path: c.path, Hash: hash, Title: c.title, Query: c.query,
			Reason: c.reason, Evidence: c.evidence, Source: source}
		if c.doc != nil {
			metadata := c.doc.Metadata
			item.Document, item.StatusMeaning = &metadata, StatusMeaning(metadata)
			item.Replacements, _ = b.catalog.Replacements(c.doc)
		}
		b.result.Items = append(b.result.Items, item)
		b.result.ContentBytes += len(source.Content)
		budget -= len(source.Content)
	}
}

type sourceRange struct{ start, end int }

func uncoveredRanges(c candidate, hash string, items []ContextItem) []sourceRange {
	ranges := []sourceRange{{c.start, c.end}}
	for _, item := range items {
		if item.Path != c.path || item.Hash != hash {
			continue
		}
		next := []sourceRange{}
		for _, span := range ranges {
			if span.end < item.Source.StartLine || span.start > item.Source.EndLine {
				next = append(next, span)
				continue
			}
			if span.start < item.Source.StartLine {
				next = append(next, sourceRange{span.start, item.Source.StartLine - 1})
			}
			if span.end > item.Source.EndLine {
				next = append(next, sourceRange{item.Source.EndLine + 1, span.end})
			}
		}
		ranges = next
	}
	return ranges
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
