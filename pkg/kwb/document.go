package kwb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	markdownast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

const (
	maxChunkLines    = 80
	maxChunkBytes    = 16 * 1024
	MaxFileBytes     = 5 * 1024 * 1024
	MaxResponseBytes = 64 * 1024
)

type document struct {
	DocumentID     string   `json:"document_id,omitempty"`
	DocumentStatus string   `json:"document_status,omitempty"`
	Collection     string   `json:"collection,omitempty"`
	Related        []string `json:"related,omitempty"`
	SupersededBy   string   `json:"superseded_by,omitempty"`
	Path           string   `json:"path"`
	Kind           string   `json:"kind"`
	Language       string   `json:"language"`
	Title          string   `json:"title"`
	Symbols        []string `json:"symbols"`
	Names          string   `json:"names"`
	Filename       string   `json:"filename"`
	Content        string   `json:"content"`
	Code           string   `json:"code"`
	Prose          string   `json:"prose"`
	StartLine      int      `json:"start_line"`
	EndLine        int      `json:"end_line"`
}

type section struct {
	start, end int // zero-based, end exclusive
	title      string
	symbols    []string
}

func fileKind(path string) (string, string) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown", ".txt", ".rst", ".adoc":
		return "documentation", strings.TrimPrefix(ext, ".")
	case ".yaml", ".yml", ".json", ".toml", ".ini", ".env", ".mod", ".sum":
		return "config", strings.TrimPrefix(ext, ".")
	case ".go",
		".js",
		".jsx",
		".ts",
		".tsx",
		".py",
		".rs",
		".java",
		".c",
		".h",
		".cpp",
		".cs",
		".rb",
		".php",
		".sh",
		".sql",
		".proto":
		return "code", strings.TrimPrefix(ext, ".")
	default:
		return "config", "text"
	}
}

func sourceLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

func chunkFile(path string, data []byte) []document {
	lines := sourceLines(string(data))
	kind, language := fileKind(path)
	var metadata *Document
	if language == "md" || language == "markdown" {
		metadata, _ = Parse(path, data)
	}
	sections := []section{{0, len(lines), filepath.Base(path), nil}}
	switch language {
	case "go":
		sections = goSections(path, data, len(lines))
	case "md", "markdown":
		sections = markdownSectionsFromDocument(metadata, lines)
	}
	var docs []document
	for _, part := range sections {
		for start := part.start; start < part.end; {
			end := start
			size := 0
			for end < part.end && end-start < maxChunkLines {
				n := len(lines[end]) + 1
				if end > start && size+n > maxChunkBytes {
					break
				}
				size += n
				end++
			}
			content := strings.Join(lines[start:end], "\n")
			// Very long single lines are split as well, retaining their source location.
			for len(content) > 0 {
				piece := utf8Prefix(content, maxChunkBytes)
				content = content[len(piece):]
				if strings.TrimSpace(piece) == "" {
					continue
				}
				doc := document{
					Path:      path,
					Kind:      kind,
					Language:  language,
					Title:     part.title,
					Symbols:   part.symbols,
					Names:     identifierWords(part.title + " " + strings.Join(part.symbols, " ")),
					Filename:  identifierWords(path),
					Content:   piece,
					StartLine: start + 1,
					EndLine:   end,
				}
				if metadata != nil {
					doc.DocumentID = metadata.ID
					doc.DocumentStatus = metadata.Status
					doc.Collection = metadata.Collection
					doc.Related = metadata.Related
					doc.SupersededBy = metadata.SupersededBy
					if doc.Title == "" {
						doc.Title = metadata.Title
					}
				}
				if kind == "documentation" {
					doc.Prose = piece
				} else {
					doc.Code = identifierWords(piece)
				}
				docs = append(docs, doc)
			}
			start = end
		}
	}
	return docs
}

func goSections(path string, data []byte, count int) []section {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		// Incomplete files are still searchable while being edited.
		return []section{{0, count, filepath.Base(path), nil}}
	}
	var sections []section
	previous := 0
	for _, decl := range file.Decls {
		start := fset.Position(decl.Pos()).Line - 1
		end := fset.Position(decl.End()).Line
		var title string
		var symbols []string
		var comment *ast.CommentGroup
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name = receiverName(d.Recv.List[0].Type) + "." + name
			}
			symbols = []string{name, d.Name.Name}
			title, comment = "func "+name, d.Doc
		case *ast.GenDecl:
			comment = d.Doc
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					symbols = append(symbols, spec.Name.Name)
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						symbols = append(symbols, name.Name)
					}
				}
			}
			title = d.Tok.String() + " " + strings.Join(symbols, ", ")
		}
		if comment != nil {
			start = fset.Position(comment.Pos()).Line - 1
		}
		if start < previous {
			start = previous
		}
		if start > previous {
			sections = append(sections, section{
				previous,
				start,
				"package " + file.Name.Name,
				nil,
			})
		}
		if end > start {
			sections = append(sections, section{
				start,
				end,
				title,
				symbols,
			})
		}
		previous = end
	}
	if previous < count {
		sections = append(sections, section{
			previous,
			count,
			"package " + file.Name.Name,
			nil,
		})
	}
	return sections
}

func receiverName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return receiverName(e.X)
	case *ast.IndexExpr:
		return receiverName(e.X)
	case *ast.IndexListExpr:
		return receiverName(e.X)
	default:
		return ""
	}
}

func markdownSectionsFromDocument(parsed *Document, lines []string) []section {
	if parsed == nil {
		return []section{{0, len(lines), "", nil}}
	}
	result := []section{}
	start, title := min(parsed.BodyStartLine-1, len(lines)), parsed.Title
	var headings [6]string
	for _, heading := range parsed.Headings {
		boundary := heading.StartLine - 1
		if boundary > start {
			result = append(result, section{
				start,
				boundary,
				title,
				nil,
			})
		}
		headings[heading.Level-1] = heading.Title
		for j := heading.Level; j < len(headings); j++ {
			headings[j] = ""
		}
		trail := []string{}
		for _, h := range headings {
			if h != "" {
				trail = append(trail, h)
			}
		}
		start, title = boundary, strings.Join(trail, " > ")
	}
	if start < len(lines) {
		result = append(result, section{
			start,
			len(lines),
			title,
			nil,
		})
	}
	return result
}

// identifierWords retains full identifiers and adds components for camelCase,
// PascalCase, acronyms and snake_case. Exact symbols are indexed separately.
func identifierWords(text string) string {
	var output strings.Builder
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) {
		output.WriteString(word)
		output.WriteByte(' ')
		for _, part := range strings.Split(word, "_") {
			if part == "" {
				continue
			}
			if part != word {
				output.WriteString(part)
				output.WriteByte(' ')
			}
			runes := []rune(part)
			start := 0
			for i := 1; i < len(runes); i++ {
				boundary := unicode.IsDigit(runes[i]) != unicode.IsDigit(runes[i-1]) ||
					unicode.IsUpper(runes[i]) && (unicode.IsLower(runes[i-1]) ||
						(i+1 < len(runes) && unicode.IsUpper(runes[i-1]) && unicode.IsLower(runes[i+1])))
				if boundary {
					output.WriteString(string(runes[start:i]))
					output.WriteByte(' ')
					start = i
				}
			}
			if start > 0 {
				output.WriteString(string(runes[start:]))
				output.WriteByte(' ')
			}
		}
	}
	return output.String()
}

func chunkID(path string, number int) string { return fmt.Sprintf("%s#%d", path, number) }

func utf8Prefix(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && text[limit]&0xc0 == 0x80 {
		limit--
	}
	return text[:limit]
}

type Metadata struct {
	ID              string   `yaml:"id"               json:"id"`
	Title           string   `yaml:"title"            json:"title"`
	Status          string   `yaml:"status"           json:"status,omitempty"`
	Date            string   `yaml:"date"             json:"date,omitempty"`
	Related         []string `yaml:"related"          json:"related,omitempty"`
	SupersededBy    string   `yaml:"superseded_by"    json:"superseded_by,omitempty"`
	SidebarPosition int      `yaml:"sidebar_position" json:"sidebar_position,omitempty"`
	Collection      string   `yaml:"collection"       json:"collection,omitempty"`
}

type Heading struct {
	Title     string `json:"title"`
	Level     int    `json:"level"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type Link struct {
	Target   string `json:"target"`
	Path     string `json:"path"`
	Fragment string `json:"fragment,omitempty"`
	Kind     string `json:"kind"`
	Line     int    `json:"line"`
}

type Diagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type Document struct {
	Metadata
	Path           string       `json:"path"`
	Hash           string       `json:"hash"`
	BodyStartLine  int          `json:"body_start_line"`
	TotalLines     int          `json:"total_lines"`
	Headings       []Heading    `json:"headings"`
	Links          []Link       `json:"links"`
	Diagnostics    []Diagnostic `json:"diagnostics,omitempty"`
	Content        string       `json:"-"`
	hasFrontMatter bool
}

func Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// Parse uses one Markdown AST for headings and links, including table cells and
// reference links. Front matter is masked before parsing, preserving line numbers.
// Metadata is optional here so ordinary Markdown can use the same parser in KWB.
func Parse(filePath string, data []byte) (*Document, error) {
	if len(data) > MaxFileBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("document must be UTF-8 text no larger than %d bytes", MaxFileBytes)
	}
	d := &Document{
		Path:          filePath,
		Hash:          Hash(data),
		Content:       string(data),
		BodyStartLine: 1,
		Headings:      []Heading{},
		Links:         []Link{},
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(data) > 0 {
		d.TotalLines = len(lines)
	}
	body := bytes.Clone(data)
	if len(lines) > 0 && strings.TrimSuffix(lines[0], "\r") == "---" {
		d.hasFrontMatter = true
		closeLine := -1
		for i := 1; i < len(lines); i++ {
			if line := strings.TrimSuffix(lines[i], "\r"); line == "---" || line == "..." {
				closeLine = i
				break
			}
		}
		if closeLine < 0 {
			return nil, fmt.Errorf("unclosed YAML front matter")
		}
		if err := yaml.Unmarshal([]byte(strings.Join(lines[1:closeLine], "\n")), &d.Metadata); err != nil {
			return nil, fmt.Errorf("invalid YAML front matter")
		}
		offset := 0
		for _, line := range lines[:closeLine+1] {
			offset += len(line) + 1
		}
		offset = min(offset, len(body))
		for i := 0; i < offset; i++ {
			if body[i] != '\n' && body[i] != '\r' {
				body[i] = ' '
			}
		}
		d.BodyStartLine = closeLine + 2
	}
	if len(d.ID) > 128 || len(d.Title) > 1024 || len(d.Related) > 128 || len(d.SupersededBy) > 128 ||
		len(d.Collection) > 128 {
		return nil, fmt.Errorf(
			"document metadata exceeds limits: id/collection/references 128 bytes, title 1024 bytes, related 128 entries",
		)
	}
	for _, id := range d.Related {
		if len(id) > 128 {
			return nil, fmt.Errorf("related ID exceeds 128 bytes")
		}
	}
	md := goldmark.New(goldmark.WithExtensions(extension.Table))
	tree := md.Parser().Parse(text.NewReader(body))
	err := markdownast.Walk(tree, func(node markdownast.Node, entering bool) (markdownast.WalkStatus, error) {
		if !entering {
			return markdownast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *markdownast.Heading:
			start := nodeLine(n, body)
			d.Headings = append(
				d.Headings,
				Heading{
					Title:     plainText(n, body),
					Level:     n.Level,
					StartLine: start,
					EndLine:   d.TotalLines,
				},
			)
		case *markdownast.Link:
			d.addLink(string(n.Destination), nodeLine(n, body))
		case *markdownast.Image:
			d.addLink(string(n.Destination), nodeLine(n, body))
		}
		return markdownast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	for i := range d.Headings {
		for j := i + 1; j < len(d.Headings); j++ {
			if d.Headings[j].Level <= d.Headings[i].Level {
				d.Headings[i].EndLine = d.Headings[j].StartLine - 1
				break
			}
		}
	}
	return d, nil
}

func plainText(node markdownast.Node, source []byte) string {
	var out strings.Builder
	_ = markdownast.Walk(node, func(n markdownast.Node, entering bool) (markdownast.WalkStatus, error) {
		if entering {
			switch n := n.(type) {
			case *markdownast.Text:
				out.Write(n.Segment.Value(source))
				if n.SoftLineBreak() || n.HardLineBreak() {
					out.WriteByte(' ')
				}
			case *markdownast.String:
				out.Write(n.Value)
			}
		}
		return markdownast.WalkContinue, nil
	})
	return out.String()
}

func nodeLine(node markdownast.Node, source []byte) int {
	offset := -1
	_ = markdownast.Walk(node, func(n markdownast.Node, entering bool) (markdownast.WalkStatus, error) {
		if entering {
			if n.Type() == markdownast.TypeBlock && n.Lines().Len() > 0 {
				offset = n.Lines().At(0).Start
				return markdownast.WalkStop, nil
			}
			if n, ok := n.(*markdownast.Text); ok {
				offset = n.Segment.Start
				return markdownast.WalkStop, nil
			}
		}
		return markdownast.WalkContinue, nil
	})
	if offset < 0 {
		if parent := node.Parent(); parent != nil {
			return nodeLine(parent, source)
		}
		return 1
	}
	return bytes.Count(source[:offset], []byte{'\n'}) + 1
}

func (d *Document) addLink(target string, line int) {
	if len(target) > 4096 {
		d.Diagnostics = append(d.Diagnostics, Diagnostic{
			"link_too_long",
			d.Path,
			"Link target exceeds 4096 bytes",
		})
		return
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return
	}
	linkPath := d.Path
	if u.Path != "" {
		linkPath = path.Join(path.Dir(d.Path), u.Path)
	}
	if strings.HasPrefix(u.Path, "/") || linkPath == ".." || strings.HasPrefix(linkPath, "../") ||
		strings.Contains(linkPath, "\\") {
		d.Diagnostics = append(
			d.Diagnostics,
			Diagnostic{
				"link_outside_project",
				d.Path,
				"Local link escapes the project root",
			},
		)
		return
	}
	kind := "source"
	if ext := strings.ToLower(path.Ext(linkPath)); ext == ".md" || ext == ".markdown" {
		kind = "document"
	} else if strings.HasSuffix(linkPath, "_test.go") || strings.HasPrefix(linkPath, "tests/") {
		kind = "test"
	}
	d.Links = append(d.Links, Link{
		Target:   target,
		Path:     linkPath,
		Fragment: u.Fragment,
		Kind:     kind,
		Line:     line,
	})
}

func (d *Document) validate() []Diagnostic {
	result := slices.Clone(d.Diagnostics)
	add := func(message string) {
		result = append(result, Diagnostic{
			"invalid_metadata",
			d.Path,
			message,
		})
	}
	if strings.TrimSpace(d.ID) == "" || len(d.ID) > 128 {
		add("id is required and must be at most 128 bytes")
	}
	if strings.TrimSpace(d.Title) == "" || len(d.Title) > 1024 {
		add("title is required and must be at most 1024 bytes")
	}
	if len(d.Related) > 128 {
		add("related exceeds 128 IDs")
	}
	switch d.Collection {
	case "requirements":
		if !slices.Contains([]string{"draft", "accepted", "retired"}, d.Status) {
			add("Requirement status must be draft, accepted, or retired")
		}
	case "decisions":
		if !slices.Contains([]string{"proposed", "accepted", "rejected", "superseded"}, d.Status) {
			add("Decision status must be proposed, accepted, rejected, or superseded")
		}
		if _, err := time.Parse("2006-01-02", d.Date); err != nil {
			add("Decision date must be YYYY-MM-DD")
		}
		if d.Status == "superseded" && d.SupersededBy == "" {
			add("Superseded decision requires superseded_by")
		}
	case "handbook":
		if d.SidebarPosition < 1 {
			add("Handbook sidebar_position must be positive")
		}
	}
	return result
}

// StatusMeaning keeps acceptance, implementation, proposals, and history distinct.
func StatusMeaning(m Metadata) string {
	if m.Collection == "templates" {
		return "Authoring template; replace its metadata and prompts before adoption"
	}
	switch m.Status {
	case "draft", "proposed":
		return "Not accepted; proposed intent does not authorize implementation"
	case "retired", "rejected", "superseded":
		return "Historical record; retain its reasoning and consult current guidance"
	case "accepted":
		if m.Collection == "requirements" {
			return "Agreed intent; acceptance does not establish implementation"
		}
		return "Accepted decision; implementation must be verified in source"
	default:
		return "Project documentation; verify implementation against current source"
	}
}
