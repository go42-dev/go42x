package kwb

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const (
	maxChunkLines = 80
	maxChunkBytes = 16 * 1024
)

type document struct {
	Path      string   `json:"path"`
	Kind      string   `json:"kind"`
	Language  string   `json:"language"`
	Title     string   `json:"title"`
	Symbols   []string `json:"symbols"`
	Names     string   `json:"names"`
	Filename  string   `json:"filename"`
	Content   string   `json:"content"`
	Code      string   `json:"code"`
	Prose     string   `json:"prose"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
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
	sections := []section{{0, len(lines), filepath.Base(path), nil}}
	switch language {
	case "go":
		sections = goSections(path, data, len(lines))
	case "md", "markdown":
		sections = markdownSections(lines)
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
					Path: path, Kind: kind, Language: language,
					Title: part.title, Symbols: part.symbols,
					Names:    identifierWords(part.title + " " + strings.Join(part.symbols, " ")),
					Filename: identifierWords(path), Content: piece,
					StartLine: start + 1, EndLine: end,
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
			sections = append(sections, section{previous, start, "package " + file.Name.Name, nil})
		}
		if end > start {
			sections = append(sections, section{start, end, title, symbols})
		}
		previous = end
	}
	if previous < count {
		sections = append(sections, section{previous, count, "package " + file.Name.Name, nil})
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

var atxHeading = regexp.MustCompile(`^ {0,3}(#{1,6})[\t ]+(.+?)\s*#*\s*$`)
var setextHeading = regexp.MustCompile(`^ {0,3}(=+|-+)\s*$`)

func markdownSections(lines []string) []section {
	var result []section
	start, title := 0, ""
	var headings [6]string
	var fence byte
	fenceSize := 0
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
			n := 0
			for n < len(trimmed) && trimmed[n] == trimmed[0] {
				n++
			}
			if n >= 3 {
				if fence == 0 {
					fence, fenceSize = trimmed[0], n
				} else if fence == trimmed[0] && n >= fenceSize {
					fence = 0
				}
				continue
			}
		}
		if fence != 0 {
			continue
		}
		level, text, boundary := 0, "", i
		if match := atxHeading.FindStringSubmatch(line); match != nil {
			level, text = len(match[1]), match[2]
		} else if i > 0 && strings.TrimSpace(lines[i-1]) != "" && setextHeading.MatchString(line) {
			level, text, boundary = 2, strings.TrimSpace(lines[i-1]), i-1
			if strings.HasPrefix(strings.TrimSpace(line), "=") {
				level = 1
			}
		}
		if level == 0 {
			continue
		}
		if boundary > start {
			result = append(result, section{start, boundary, title, nil})
		}
		headings[level-1] = text
		for j := level; j < len(headings); j++ {
			headings[j] = ""
		}
		var trail []string
		for _, heading := range headings {
			if heading != "" {
				trail = append(trail, heading)
			}
		}
		start, title = boundary, strings.Join(trail, " > ")
	}
	if start < len(lines) {
		result = append(result, section{start, len(lines), title, nil})
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
