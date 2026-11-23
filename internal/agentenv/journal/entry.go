// Package journal keeps a record of each agent task in the project's journal directory, so
// any runtime can find, continue, and clean up the task's material.
package journal

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/config"
)

// Runtimes that are not agent providers.
const (
	RuntimeHuman   = "human"
	RuntimeUnknown = "unknown"
)

const (
	entryVersion   = 2
	entryFile      = "entry.json"
	notesFile      = "notes.md"
	logFile        = "log.jsonl"
	indexFile      = ".index.json"
	lockFile       = ".lock"
	schemaPath     = "schemas/journal-entry.schema.json"
	schemaLinkPath = "assets/agentenv/" + schemaPath
	notesTemplate  = "skills/go42x-journal/notes.md"
	maxTags        = 8
	maxTitleLength = 200
	maxSlugLength  = 40
)

var (
	idPattern       = regexp.MustCompile(`^[0-9]{8}-[a-z0-9]+(?:-[a-z0-9]+)*-[0-9a-f]{4}$`)
	tagPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	slugPattern     = regexp.MustCompile(`[^a-z0-9]+`)
	fileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	commentPattern  = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// Entry is the record of one task in entry.json.
type Entry struct {
	Schema    string    `json:"$schema,omitempty"`
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Tags      []string  `json:"tags"`
	Keep      bool      `json:"keep"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Runtime   string    `json:"runtime"`
	Session   string    `json:"session,omitempty"`
	Refs      Refs      `json:"refs"`
}

// Expired reports whether gc deletes the entry at now: it is not kept and went without an
// update for the retention period.
func (e *Entry) Expired(now time.Time, retention time.Duration) bool {
	return !e.Keep && !e.UpdatedAt.Add(retention).After(now)
}

// Refs names the work an entry belongs to, as recorded at its last write.
type Refs struct {
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	PR     string `json:"pr,omitempty"`
	Issue  string `json:"issue,omitempty"`
}

// Event is one line of log.jsonl.
type Event struct {
	At      time.Time `json:"at"`
	Event   string    `json:"event"`
	Runtime string    `json:"runtime"`
	Session string    `json:"session,omitempty"`
	Summary string    `json:"summary,omitempty"`
}

// NoteSection is one "## " section of an entry's notes. Text before the first section,
// other than the title, has an empty heading.
type NoteSection struct {
	Heading string `json:"heading"`
	Text    string `json:"text"`
}

// Actor identifies the runtime and session that change an entry.
type Actor struct {
	Runtime string `json:"runtime"`
	Session string `json:"session,omitempty"`
}

// Runtimes returns the runtime names an entry can record: the agent providers, a human,
// and an unknown runtime.
func Runtimes() []string {
	return append(config.ProviderNames(), RuntimeHuman, RuntimeUnknown)
}

// Validate checks an actor before it is recorded.
func (a Actor) Validate() error {
	if !slices.Contains(Runtimes(), a.Runtime) {
		return fmt.Errorf("runtime must be one of %s", strings.Join(Runtimes(), ", "))
	}
	if utf8.RuneCountInString(a.Session) > 200 || strings.ContainsFunc(a.Session, isControl) {
		return fmt.Errorf("session must be at most 200 characters without control characters")
	}
	return nil
}

// ValidateID rejects identifiers that are not entry directory names, including any path.
func ValidateID(id string) error {
	if !idPattern.MatchString(id) || len(id) > 64 {
		return fmt.Errorf("invalid journal entry ID %q", id)
	}
	return nil
}

// NormalizeTags lowercases tags and checks the tag rules: at most eight, without
// duplicates, each lowercase letters, digits, and hyphens.
func NormalizeTags(tags []string) ([]string, error) {
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if !tagPattern.MatchString(tag) {
			return nil, fmt.Errorf("invalid tag %q: use up to 32 lowercase letters, digits, and hyphens", tag)
		}
		if slices.Contains(normalized, tag) {
			return nil, fmt.Errorf("duplicate tag %q", tag)
		}

		normalized = append(normalized, tag)
	}
	if len(normalized) > maxTags {
		return nil, fmt.Errorf("at most %d tags are allowed", maxTags)
	}
	return normalized, nil
}

func validateTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if len(title) == 0 || utf8.RuneCountInString(title) > maxTitleLength || strings.ContainsFunc(title, isControl) {
		return "", fmt.Errorf("title must be 1 to %d characters without control characters", maxTitleLength)
	}
	return title, nil
}

func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f
}

// newID derives an entry ID from the creation date and title, with a random suffix.
func newID(created time.Time, title string) (string, error) {
	slug := strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > maxSlugLength {
		slug = strings.TrimRight(slug[:maxSlugLength], "-")
	}
	if len(slug) == 0 {
		slug = "entry"
	}

	suffix := make([]byte, 2)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return created.UTC().Format("20060102") + "-" + slug + "-" + hex.EncodeToString(suffix), nil
}

// renderNotes returns the starter notes.md of an entry, from the go42x-journal skill's template.
func renderNotes(title string) ([]byte, error) {
	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		return nil, err
	}
	template, err := fs.ReadFile(bundle, notesTemplate)
	if err != nil {
		return nil, fmt.Errorf("read notes template: %w", err)
	}
	return []byte(strings.NewReplacer("{{.Title}}", title).Replace(string(template))), nil
}

// noteSections splits notes at their "## " headings, outside fenced code. It leaves out the
// title, the template's comments, and sections without text.
func noteSections(notes string) []NoteSection {
	var sections []NoteSection
	current := NoteSection{}
	var text []string
	flush := func() {
		current.Text = strings.TrimSpace(strings.Join(text, "\n"))
		if len(current.Text) > 0 {
			sections = append(sections, current)
		}
	}

	// fence is the opening run of backticks or tildes while inside fenced code; a run of the
	// same character, at least as long and alone on its line, closes it.
	fence := ""
	titled := false
	for line := range strings.SplitSeq(commentPattern.ReplaceAllString(notes, ""), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case len(fence) > 0:
			if strings.HasPrefix(trimmed, fence) && len(strings.Trim(trimmed, fence[:1])) == 0 {
				fence = ""
			}
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			fence = trimmed[:len(trimmed)-len(strings.TrimLeft(trimmed, trimmed[:1]))]
		case strings.HasPrefix(line, "## "):
			flush()
			current, text = NoteSection{Heading: strings.TrimSpace(line[3:])}, nil
			continue
		case strings.HasPrefix(line, "# ") && !titled && len(current.Heading) == 0 && len(sections) == 0:
			titled = true
			continue
		}
		text = append(text, line)
	}
	flush()
	return sections
}

// schemaLink returns the schema link that entries written by this binary carry.
func schemaLink() string {
	return buildinfo.SourceURL(schemaLinkPath)
}

var compileSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(bundle, schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read journal entry schema: %w", err)
	}

	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse journal entry schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaPath, document); err != nil {
		return nil, err
	}
	return compiler.Compile(schemaPath)
})

// decodeEntry parses and validates entry.json for the entry directory dir.
func decodeEntry(data []byte, dir string) (*Entry, error) {
	schema, err := compileSchema()
	if err != nil {
		return nil, err
	}

	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", entryFile, err)
	}
	if err := schema.Validate(document); err != nil {
		return nil, fmt.Errorf("%s does not match the journal entry schema: %w", entryFile, err)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("parse %s: %w", entryFile, err)
	}
	if entry.ID != dir {
		return nil, fmt.Errorf("%s names entry %q, but its directory is %q", entryFile, entry.ID, dir)
	}
	return &entry, nil
}

// encodeEntry renders entry.json after checking it against the schema.
func encodeEntry(entry *Entry) ([]byte, error) {
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := decodeEntry(data, entry.ID); err != nil {
		return nil, err
	}
	return data, nil
}
