package journal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// maxSearchLines bounds the matching notes lines returned for each search result.
const maxSearchLines = 3

type Service struct {
	logger   *slog.Logger
	settings *Settings
	store    store
}

// EntryResult reports an entry and its directory relative to the project root.
type EntryResult struct {
	Entry *Entry `json:"entry"`
	Path  string `json:"path"`
}

// EntryFile is a file in an entry directory, relative to the project root.
type EntryFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type NewOptions struct {
	Title string
	Tags  []string
	PR    string
	Issue string
	// Keep exempts the entry from gc.
	Keep bool
}

type ListOptions struct {
	Tags []string
	// Since leaves out entries last updated before it when not zero.
	Since time.Time
	// Kept lists only entries that gc never deletes.
	Kept bool
	// PR and Issue list only entries with a matching reference when not empty. A number,
	// with or without "#", also matches a URL that ends with it.
	PR    string
	Issue string
	// Stray lists content without an entry record instead of entries.
	Stray bool
}

type ListResult struct {
	Entries []*Entry    `json:"entries"`
	Invalid []Problem   `json:"invalid"`
	Stray   []StrayItem `json:"stray,omitempty"`
}

type ShowOptions struct {
	ID string
	// Since leaves out log events from before it when not zero.
	Since time.Time
	// Sections returns the notes split by section instead of as a whole.
	Sections bool
}

type ShowResult struct {
	Entry    *Entry        `json:"entry"`
	Path     string        `json:"path"`
	Notes    string        `json:"notes,omitempty"`
	Sections []NoteSection `json:"sections,omitempty"`
	Events   []Event       `json:"events"`
	Files    []EntryFile   `json:"files"`
}

type AttachOptions struct {
	ID string
	// Path is the file to copy, absolute or relative to the project root.
	Path string
	// Name is the file name of the copy in the entry; the source's name when empty.
	Name string
}

type NoteOptions struct {
	ID      string
	Summary string
	// Tags replace the recorded tags when not nil.
	Tags  []string
	PR    string
	Issue string
	// Keep exempts the entry from gc, or no longer does, when not nil.
	Keep *bool
}

type SearchOptions struct {
	Query string
	Limit int
}

type SearchResult struct {
	Matches []SearchMatch `json:"matches"`
}

// SearchMatch is an entry whose record or notes contain the query.
type SearchMatch struct {
	Entry *Entry   `json:"entry"`
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
}

type IndexResult struct {
	Path    string `json:"path"`
	Entries int    `json:"entries"`
}

type GCOptions struct {
	Apply bool
}

type GCResult struct {
	Applied bool     `json:"applied"`
	Entries []GCItem `json:"entries"`
	Size    int64    `json:"size"`
}

// GCItem is an entry that went without an update for the retention period.
type GCItem struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
}

func (o NewOptions) Validate() error {
	if _, err := validateTitle(o.Title); err != nil {
		return err
	}
	if _, err := NormalizeTags(o.Tags); err != nil {
		return err
	}
	return validateReferences(o.PR, o.Issue)
}

func (o ListOptions) Validate() error {
	if o.Stray && (len(o.Tags) > 0 || !o.Since.IsZero() || o.Kept || len(o.PR) > 0 || len(o.Issue) > 0) {
		return fmt.Errorf("stray listing does not take entry filters")
	}
	if _, err := NormalizeTags(o.Tags); err != nil {
		return err
	}
	return validateReferences(o.PR, o.Issue)
}

func (o ShowOptions) Validate() error {
	return ValidateID(o.ID)
}

func (o AttachOptions) Validate() error {
	if err := ValidateID(o.ID); err != nil {
		return err
	}
	if len(strings.TrimSpace(o.Path)) == 0 || strings.ContainsFunc(o.Path, isControl) {
		return fmt.Errorf("the file to attach is required, without control characters")
	}
	if len(o.Name) > 0 {
		return validateFileName(o.Name)
	}
	return nil
}

func (o NoteOptions) Validate() error {
	if err := ValidateID(o.ID); err != nil {
		return err
	}
	if o.Tags != nil {
		if _, err := NormalizeTags(o.Tags); err != nil {
			return err
		}
	}
	if err := validateSummary(o.Summary); err != nil {
		return err
	}
	return validateReferences(o.PR, o.Issue)
}

func (o SearchOptions) Validate() error {
	if len(strings.TrimSpace(o.Query)) == 0 {
		return fmt.Errorf("search query is required")
	}
	if o.Limit < 0 {
		return fmt.Errorf("limit must not be negative")
	}
	return nil
}

func validateSummary(summary string) error {
	if utf8.RuneCountInString(summary) > 1000 || strings.ContainsFunc(summary, isControl) {
		return fmt.Errorf("summary must be at most 1000 characters without control characters")
	}
	return nil
}

// validateFileName accepts the name of a file that attach copies into an entry: a plain
// name that is neither hidden, like temporary files, nor one of the files go42x keeps.
func validateFileName(name string) error {
	if !fileNamePattern.MatchString(name) {
		return fmt.Errorf(
			"invalid file name %q: use up to 128 letters, digits, dots, underscores, and hyphens, "+
				"starting with a letter or digit", name)
	}
	if slices.Contains([]string{entryFile, notesFile, logFile}, name) {
		return fmt.Errorf("%s is kept by go42x; choose another name", name)
	}
	return nil
}

// ParseSince parses the RFC 3339 time of a since filter. An empty value returns the zero
// time, which filters nothing.
func ParseSince(value string) (time.Time, error) {
	if len(value) == 0 {
		return time.Time{}, nil
	}
	since, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("since must be an RFC 3339 time, such as 2026-11-10T09:00:00Z")
	}
	return since, nil
}

func validateReferences(refs ...string) error {
	for _, ref := range refs {
		if utf8.RuneCountInString(ref) > 2048 || strings.ContainsFunc(ref, isControl) {
			return fmt.Errorf(
				"pull request and issue references must be at most 2048 characters without control characters",
			)
		}
	}
	return nil
}

func NewService(settings *Settings, opts ...Option) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("invalid settings: %w", err)
	}

	root, err := filepath.Abs(settings.RootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	store, err := loadStore(root)
	if err != nil {
		return nil, err
	}

	svc := &Service{
		settings: settings,
		store:    store,
	}
	for _, opt := range opts {
		opt(svc)
	}
	if svc.logger == nil {
		svc.logger = slog.New(slog.DiscardHandler)
	}
	svc.logger = svc.logger.With(slog.String("component", "journal"))
	return svc, nil
}

// New creates an entry with an empty notes template. When a step fails, the entry directory
// is removed again.
func (s *Service) New(ctx context.Context, actor Actor, options NewOptions) (_ *EntryResult, retErr error) {
	if err := actor.Validate(); err != nil {
		return nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}

	title := strings.TrimSpace(options.Title)
	tags, err := NormalizeTags(options.Tags)
	if err != nil {
		return nil, err
	}
	notes, err := renderNotes(title)
	if err != nil {
		return nil, err
	}

	unlock, err := s.store.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock() //nolint:errcheck

	now := timestamp()
	entry := &Entry{
		Schema:    schemaLink(),
		Version:   entryVersion,
		Title:     title,
		Tags:      tags,
		Keep:      options.Keep,
		CreatedAt: now,
		UpdatedAt: now,
		Runtime:   actor.Runtime,
		Session:   actor.Session,
		Refs:      s.currentRefs(ctx, Refs{PR: options.PR, Issue: options.Issue}),
	}
	for {
		entry.ID, err = newID(now, title)
		if err != nil {
			return nil, err
		}

		err = os.Mkdir(s.store.entryDir(entry.ID), 0700)
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("create journal entry: %w", err)
	}
	created := false
	defer func() {
		if !created {
			retErr = errors.Join(retErr, os.RemoveAll(s.store.entryDir(entry.ID)))
		}
	}()

	if err := s.store.write(entry); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(s.store.entryDir(entry.ID), notesFile), notes); err != nil {
		return nil, err
	}
	if err := s.record(ctx, entry, now, "created", actor, ""); err != nil {
		return nil, err
	}
	created = true
	return s.entryResult(entry), nil
}

// List returns entries, most recently updated first, that match every supplied filter.
func (s *Service) List(ctx context.Context, options ListOptions) (*ListResult, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}

	tags, err := NormalizeTags(options.Tags)
	if err != nil {
		return nil, err
	}

	current, err := s.store.scan(ctx)
	if err != nil {
		return nil, err
	}
	if options.Stray {
		return &ListResult{
			Entries: []*Entry{},
			Invalid: current.invalid,
			Stray:   current.stray,
		}, nil
	}

	entries := []*Entry{}
	for _, entry := range current.entries {
		if entry.UpdatedAt.Before(options.Since) ||
			(options.Kept && !entry.Keep) ||
			!matchReference(entry.Refs.PR, options.PR) ||
			!matchReference(entry.Refs.Issue, options.Issue) ||
			slices.ContainsFunc(tags, func(tag string) bool { return !slices.Contains(entry.Tags, tag) }) {
			continue
		}
		entries = append(entries, entry)
	}
	return &ListResult{
		Entries: entries,
		Invalid: current.invalid,
	}, nil
}

// Show returns an entry with its notes, log, and files.
func (s *Service) Show(_ context.Context, options ShowOptions) (*ShowResult, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}

	entry, err := s.store.read(options.ID)
	if err != nil {
		return nil, err
	}

	notes, err := os.ReadFile(filepath.Join(s.store.entryDir(options.ID), notesFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	events, err := s.store.events(options.ID)
	if err != nil {
		return nil, err
	}
	events = slices.DeleteFunc(events, func(event Event) bool { return event.At.Before(options.Since) })
	files, err := s.store.entryFiles(options.ID)
	if err != nil {
		return nil, err
	}

	result := &ShowResult{
		Entry:  entry,
		Path:   s.store.relative(s.store.entryDir(options.ID)),
		Events: events,
		Files:  files,
	}
	if options.Sections {
		result.Sections = noteSections(string(notes))
	} else {
		result.Notes = string(notes)
	}
	return result, nil
}

// Attach copies a file from the project into an entry directory. It never replaces a file
// and records nothing: like other files an agent keeps in an entry, the copy becomes part of
// the record with the next note.
func (s *Service) Attach(ctx context.Context, options AttachOptions) (*EntryFile, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}

	source, err := s.store.projectFile(options.Path)
	if err != nil {
		return nil, err
	}
	name := options.Name
	if len(name) == 0 {
		name = filepath.Base(source)
		if err := validateFileName(name); err != nil {
			return nil, fmt.Errorf("%w; name the copy with a valid file name", err)
		}
	}

	unlock, err := s.store.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock() //nolint:errcheck

	if _, err := s.store.read(options.ID); err != nil {
		return nil, err
	}
	target := filepath.Join(s.store.entryDir(options.ID), name)
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("journal entry %s already has %s; choose another name", options.ID, name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// #nosec G304 -- projectFile resolved the source to a regular file inside the project.
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(target, data); err != nil {
		return nil, err
	}
	return &EntryFile{
		Path: s.store.relative(target),
		Size: int64(len(data)),
	}, nil
}

// Note records an update to an entry's notes.
func (s *Service) Note(ctx context.Context, actor Actor, options NoteOptions) (*EntryResult, error) {
	if err := actor.Validate(); err != nil {
		return nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}

	var tags []string
	if options.Tags != nil {
		var err error
		tags, err = NormalizeTags(options.Tags)
		if err != nil {
			return nil, err
		}
	}

	unlock, err := s.store.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock() //nolint:errcheck

	entry, err := s.store.read(options.ID)
	if err != nil {
		return nil, err
	}

	if tags != nil {
		entry.Tags = tags
	}
	if options.Keep != nil {
		entry.Keep = *options.Keep
	}
	entry.Refs = s.currentRefs(ctx, mergeRefs(entry.Refs, Refs{PR: options.PR, Issue: options.Issue}))
	if err := s.record(ctx, entry, timestamp(), "note", actor, options.Summary); err != nil {
		return nil, err
	}
	return s.entryResult(entry), nil
}

// Search returns entries whose title, ID, tags, or notes contain the query, ignoring case.
func (s *Service) Search(ctx context.Context, options SearchOptions) (*SearchResult, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}

	query := strings.ToLower(strings.TrimSpace(options.Query))

	current, err := s.store.scan(ctx)
	if err != nil {
		return nil, err
	}

	matches := []SearchMatch{}
	for _, entry := range current.entries {
		if options.Limit > 0 && len(matches) >= options.Limit {
			break
		}

		lines := []string{}
		notes, err := os.ReadFile(filepath.Join(s.store.entryDir(entry.ID), notesFile))
		if err == nil {
			for line := range strings.SplitSeq(string(notes), "\n") {
				// Comments hold the template's guidance, which every entry shares.
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "<!--") {
					continue
				}
				if len(lines) < maxSearchLines && strings.Contains(strings.ToLower(line), query) {
					lines = append(lines, line)
				}
			}
		}
		if len(lines) == 0 &&
			!strings.Contains(strings.ToLower(entry.Title), query) &&
			!strings.Contains(entry.ID, query) &&
			!slices.ContainsFunc(entry.Tags, func(tag string) bool { return strings.Contains(tag, query) }) {
			continue
		}

		matches = append(matches, SearchMatch{
			Entry: entry,
			Path:  s.store.relative(s.store.entryDir(entry.ID)),
			Lines: lines,
		})
	}
	return &SearchResult{Matches: matches}, nil
}

// Index rebuilds the generated index of the journal.
func (s *Service) Index(ctx context.Context) (*IndexResult, error) {
	unlock, err := s.store.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock() //nolint:errcheck

	count, err := s.store.writeIndex(ctx)
	if err != nil {
		return nil, err
	}
	return &IndexResult{
		Path:    s.store.relative(filepath.Join(s.store.dir, indexFile)),
		Entries: count,
	}, nil
}

// GC reports entries that went without an update for the retention period and, with Apply,
// deletes them. Kept entries and content without a record are never deleted. A failed
// deletion does not stop the others; the index is rebuilt either way.
func (s *Service) GC(ctx context.Context, options GCOptions) (*GCResult, error) {
	unlock, err := s.store.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock() //nolint:errcheck

	current, err := s.store.scan(ctx)
	if err != nil {
		return nil, err
	}

	result := &GCResult{
		Applied: options.Apply,
		Entries: []GCItem{},
	}
	now := time.Now()
	var errs []error
	for _, entry := range current.entries {
		if !entry.Expired(now, s.store.retention) {
			continue
		}

		dir := s.store.entryDir(entry.ID)
		item := GCItem{
			ID:        entry.ID,
			Title:     entry.Title,
			UpdatedAt: entry.UpdatedAt,
			Path:      s.store.relative(dir),
			Size:      treeSize(dir),
		}
		if options.Apply {
			if err := s.store.remove(entry.ID); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		result.Entries = append(result.Entries, item)
		result.Size += item.Size
	}
	if options.Apply {
		if _, err := s.store.writeIndex(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return result, errors.Join(errs...)
}

// record stamps, stores, and logs a change made at now, then refreshes the index.
func (s *Service) record(
	ctx context.Context,
	entry *Entry,
	now time.Time,
	event string,
	actor Actor,
	summary string,
) error {
	entry.Schema = schemaLink()
	entry.UpdatedAt = now
	entry.Runtime = actor.Runtime
	entry.Session = actor.Session
	if err := s.store.write(entry); err != nil {
		return err
	}

	if err := s.store.appendEvent(entry.ID, Event{
		At:      entry.UpdatedAt,
		Event:   event,
		Runtime: actor.Runtime,
		Session: actor.Session,
		Summary: strings.TrimSpace(summary),
	}); err != nil {
		return err
	}

	_, err := s.store.writeIndex(ctx)
	return err
}

func (s *Service) entryResult(entry *Entry) *EntryResult {
	return &EntryResult{
		Entry: entry,
		Path:  s.store.relative(s.store.entryDir(entry.ID)),
	}
}

// timestamp returns the current time in UTC, rounded to seconds for readable records.
func timestamp() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

// matchReference reports whether a recorded pull request or issue reference matches a list
// filter. An empty filter matches every reference. A number, with or without "#", also
// matches a reference that ends with "/" and the number, such as an issue URL.
func matchReference(recorded, filter string) bool {
	filter = strings.TrimSpace(filter)
	if len(filter) == 0 || recorded == filter {
		return true
	}
	number := strings.TrimPrefix(filter, "#")
	if len(number) == 0 || strings.ContainsFunc(number, func(r rune) bool { return r < '0' || r > '9' }) {
		return false
	}
	return recorded == number || recorded == "#"+number || strings.HasSuffix(recorded, "/"+number)
}

func mergeRefs(recorded, update Refs) Refs {
	if len(update.PR) > 0 {
		recorded.PR = update.PR
	}
	if len(update.Issue) > 0 {
		recorded.Issue = update.Issue
	}
	return recorded
}
