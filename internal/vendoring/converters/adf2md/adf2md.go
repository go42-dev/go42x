// Package adf2md renders Atlassian Document Format (ADF) as GitHub Flavored Markdown.
// @see https://developer.atlassian.com/cloud/jira/platform/apis/document/structure/
package adf2md

import (
	"encoding/json"
	"fmt"
	"html"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const ADFToMarkdown = "adf-to-markdown"

const (
	nodeBulletList   = "bulletList"
	nodeOrderedList  = "orderedList"
	nodeTaskList     = "taskList"
	nodeDecisionList = "decisionList"
	nodeTableHeader  = "tableHeader"
	nodeText         = "text"
)

// alerts maps panel types to GitHub alert types with a similar meaning and color.
var alerts = map[string]string{
	"info":    "NOTE",
	"note":    "IMPORTANT",
	"tip":     "TIP",
	"success": "TIP",
	"warning": "WARNING",
	"error":   "CAUTION",
}

var (
	lineStartPattern = regexp.MustCompile(`^([#>+=-])`)
	numberedPattern  = regexp.MustCompile(`^([0-9]{1,9})([.)])`)
	languagePattern  = regexp.MustCompile(`^[A-Za-z0-9_+#.-]+$`)
	cellBreakPattern = regexp.MustCompile(`\n+`)
)

type ADFToMarkdownConverter struct{}

func NewADFToMarkdownConverter() *ADFToMarkdownConverter {
	return &ADFToMarkdownConverter{}
}

// Convert renders an ADF document. Content without a Markdown equivalent keeps its text;
// macros without content become HTML comments, and attachments become placeholders.
func (c *ADFToMarkdownConverter) Convert(data []byte) ([]byte, error) {
	var doc node
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decode ADF: %w", err)
	}
	if doc.Type != "doc" {
		return nil, fmt.Errorf("expected an ADF doc node, got %q", doc.Type)
	}

	return []byte(strings.TrimSpace(renderBlocks(doc.Content)) + "\n"), nil
}

type node struct {
	Type    string         `json:"type"`
	Attrs   map[string]any `json:"attrs"`
	Content []node         `json:"content"`
	Marks   []mark         `json:"marks"`
	Text    string         `json:"text"`
}

type mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

func (n node) attr(name string) string {
	return attrString(n.Attrs, name)
}

func attrString(attrs map[string]any, name string) string {
	switch value := attrs[name].(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
}

func (n node) inline() bool {
	switch n.Type {
	case nodeText, "hardBreak", "mention", "emoji", "date", "status", "inlineCard", "inlineExtension",
		"placeholder", "mediaInline":
		return true
	default:
		return false
	}
}

// renderBlocks separates blocks with blank lines. Inline runs outside a paragraph,
// such as the content of task items, render as one paragraph.
func renderBlocks(nodes []node) string {
	return joinBlocks(nodes, false)
}

// joinBlocks renders blocks; tight places nested lists directly below the preceding block,
// as list items do.
func joinBlocks(nodes []node, tight bool) string {
	type rendered struct {
		text string
		list bool
	}
	var blocks []rendered
	for i := 0; i < len(nodes); i++ {
		var text string
		if nodes[i].inline() {
			end := i
			for end < len(nodes) && nodes[end].inline() {
				end++
			}
			text = escapeLineStarts(renderInline(nodes[i:end]))
			i = end - 1
		} else {
			text = renderBlock(nodes[i])
		}
		if len(strings.TrimSpace(text)) == 0 {
			continue
		}

		blocks = append(blocks, rendered{
			text: text,
			list: isList(nodes[i].Type),
		})
	}

	var b strings.Builder
	for i, block := range blocks {
		if i > 0 {
			if tight && block.list {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(block.text)
	}
	return b.String()
}

func isList(kind string) bool {
	return kind == nodeBulletList || kind == nodeOrderedList || kind == nodeTaskList || kind == nodeDecisionList
}

func renderBlock(n node) string {
	switch n.Type {
	case "paragraph":
		return escapeLineStarts(renderInline(n.Content))
	case "heading":
		level, err := strconv.Atoi(n.attr("level"))
		if err != nil || level < 1 || level > 6 {
			level = 1
		}
		text := renderInline(n.Content)
		if len(strings.TrimSpace(text)) == 0 {
			return ""
		}
		return strings.Repeat("#", level) + " " + text
	case nodeBulletList:
		return renderList(n.Content, func(int) string {
			return "- "
		})
	case nodeOrderedList:
		start, err := strconv.Atoi(n.attr("order"))
		if err != nil || start < 0 {
			start = 1
		}
		return renderList(n.Content, func(i int) string {
			return strconv.Itoa(start+i) + ". "
		})
	case nodeTaskList, nodeDecisionList:
		return renderChecklist(n)
	case "codeBlock":
		return renderCode(n)
	case "blockquote":
		return quote(renderBlocks(n.Content))
	case "rule":
		return "---"
	case "panel":
		alert, ok := alerts[n.attr("panelType")]
		if !ok {
			alert = "NOTE"
		}
		return quote("[!" + alert + "]\n" + renderBlocks(n.Content))
	case "expand", "nestedExpand":
		return "<details>\n<summary>" + html.EscapeString(n.attr("title")) + "</summary>\n\n" +
			renderBlocks(n.Content) + "\n\n</details>"
	case "table":
		return renderTable(n)
	case "mediaSingle", "mediaGroup":
		return renderMedia(n)
	case "blockCard", "embedCard":
		return link(n.attr("url"))
	case "extension":
		return macroComment(n)
	default:
		// Layouts, bodied macros, and unknown containers keep their content.
		return renderBlocks(n.Content)
	}
}

func renderList(items []node, marker func(int) string) string {
	lines := make([]string, 0, len(items))
	for i, item := range items {
		lines = append(lines, indent(marker(i), "", joinBlocks(item.Content, true)))
	}
	return strings.Join(lines, "\n")
}

// renderChecklist renders task and decision items. Nested lists follow the item they belong to.
func renderChecklist(n node) string {
	var lines []string
	for _, child := range n.Content {
		switch child.Type {
		case "taskItem":
			box := "[ ] "
			if child.attr("state") == "DONE" {
				box = "[x] "
			}
			lines = append(lines, indent("- ", box, joinBlocks(child.Content, true)))
		case "decisionItem":
			lead := ""
			if child.attr("state") == "DECIDED" {
				lead = "✓ "
			}
			lines = append(lines, indent("- ", lead, joinBlocks(child.Content, true)))
		case nodeTaskList, nodeDecisionList:
			lines = append(lines, indent("  ", "", renderChecklist(child)))
		}
	}
	return strings.Join(lines, "\n")
}

// indent writes body after a list marker and lead text, such as a task box. Continuation
// lines align with the end of the marker, where CommonMark starts the item's content.
func indent(marker, lead, body string) string {
	padding := strings.Repeat(" ", len(marker))
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		switch {
		case i == 0:
			lines[i] = marker + lead + line
		case len(line) > 0:
			lines[i] = padding + line
		}
	}
	return strings.Join(lines, "\n")
}

func quote(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if len(line) == 0 {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

func renderCode(n node) string {
	var code strings.Builder
	for _, child := range n.Content {
		code.WriteString(child.Text)
	}

	language := n.attr("language")
	if !languagePattern.MatchString(language) {
		language = ""
	}
	fence := strings.Repeat("`", max(3, longestRun(code.String(), '`')+1))
	return fence + language + "\n" + code.String() + "\n" + fence
}

func longestRun(text string, r rune) int {
	longest, current := 0, 0
	for _, c := range text {
		if c == r {
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return longest
}

// renderTable writes a GitHub table. A table without a header row gets an empty one,
// because GitHub tables require a header.
func renderTable(n node) string {
	var rows [][]string
	header := false
	for _, row := range n.Content {
		if row.Type != "tableRow" {
			continue
		}

		var cells []string
		headerRow := true
		for _, cell := range row.Content {
			cells = append(cells, cellText(cell))
			span, err := strconv.Atoi(cell.attr("colspan"))
			for i := 1; err == nil && i < span; i++ {
				cells = append(cells, "")
			}
			headerRow = headerRow && cell.Type == nodeTableHeader
		}
		if len(rows) == 0 {
			header = headerRow && len(cells) > 0
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return ""
	}

	width := 0
	for _, row := range rows {
		width = max(width, len(row))
	}
	if !header {
		rows = append([][]string{make([]string, width)}, rows...)
	}

	separator := make([]string, width)
	for i := range separator {
		separator[i] = "---"
	}
	lines := make([]string, 0, len(rows)+1)
	for i, row := range rows {
		lines = append(lines, tableRow(row, width))
		if i == 0 {
			lines = append(lines, tableRow(separator, width))
		}
	}
	return strings.Join(lines, "\n")
}

func tableRow(cells []string, width int) string {
	padded := make([]string, width)
	copy(padded, cells)
	return "| " + strings.Join(padded, " | ") + " |"
}

// cellText renders cell blocks on one line, with breaks between blocks.
func cellText(cell node) string {
	text := strings.ReplaceAll(renderBlocks(cell.Content), "|", `\|`)
	text = strings.ReplaceAll(text, "\\\n", "\n")
	return cellBreakPattern.ReplaceAllString(strings.TrimSpace(text), "<br>")
}

func renderMedia(n node) string {
	var parts []string
	for _, child := range n.Content {
		switch child.Type {
		case "media":
			parts = append(parts, mediaText(child))
		case "caption":
			if caption := renderInline(child.Content); len(caption) > 0 {
				parts = append(parts, "*"+caption+"*")
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// mediaText links external images. Attachments need a separate download and become placeholders.
func mediaText(n node) string {
	alt := escapeText(n.attr("alt"))
	if url := n.attr("url"); n.attr("type") == "external" && len(url) > 0 {
		return "![" + alt + "](" + destination(url) + ")"
	}
	if len(alt) > 0 {
		return "*[attachment: " + alt + "]*"
	}
	return "*[attachment]*"
}

func macroComment(n node) string {
	key := strings.ReplaceAll(n.attr("extensionKey"), "--", "-")
	return "<!-- Confluence macro " + strconv.Quote(key) + " is not rendered -->"
}

func renderInline(nodes []node) string {
	var b strings.Builder
	for _, n := range mergeText(nodes) {
		switch n.Type {
		case nodeText:
			b.WriteString(renderText(n))
		case "hardBreak":
			b.WriteString("\\\n")
		case "mention":
			b.WriteString(escapeText(n.attr("text")))
		case "emoji":
			if text := n.attr("text"); len(text) > 0 {
				b.WriteString(text)
			} else {
				b.WriteString(escapeText(n.attr("shortName")))
			}
		case "date":
			b.WriteString(formatDate(n.attr("timestamp")))
		case "status":
			if text := strings.TrimSpace(n.attr("text")); len(text) > 0 {
				b.WriteString("**" + escapeText(text) + "**")
			}
		case "inlineCard":
			b.WriteString(link(n.attr("url")))
		case "inlineExtension":
			b.WriteString(macroComment(n))
		case "mediaInline":
			b.WriteString(mediaText(n))
		case "placeholder":
		default:
			b.WriteString(renderInline(n.Content))
		}
	}
	return b.String()
}

// mergeText joins adjacent text with the same marks, so emphasis is not split.
func mergeText(nodes []node) []node {
	merged := make([]node, 0, len(nodes))
	for _, n := range nodes {
		last := len(merged) - 1
		if n.Type == nodeText && last >= 0 && merged[last].Type == nodeText &&
			reflect.DeepEqual(merged[last].Marks, n.Marks) {
			merged[last].Text += n.Text
			continue
		}
		merged = append(merged, n)
	}
	return merged
}

func renderText(n node) string {
	var code, strike, em, strong bool
	href := ""
	for _, m := range n.Marks {
		switch m.Type {
		case "code":
			code = true
		case "strike":
			strike = true
		case "em":
			em = true
		case "strong":
			strong = true
		case "link":
			href = attrString(m.Attrs, "href")
		}
	}

	text := escapeText(n.Text)
	if code {
		text = codeSpan(n.Text)
	}
	if strike {
		text = wrap(text, "~~")
	}
	if em {
		text = wrap(text, "*")
	}
	if strong {
		text = wrap(text, "**")
	}
	if len(href) > 0 {
		text = "[" + text + "](" + destination(href) + ")"
	}
	return text
}

// wrap places delimiters around text, keeping surrounding spaces outside so emphasis applies.
func wrap(text, delimiter string) string {
	core := strings.TrimSpace(text)
	if len(core) == 0 {
		return text
	}

	start := strings.Index(text, core)
	return text[:start] + delimiter + core + delimiter + text[start+len(core):]
}

func codeSpan(text string) string {
	ticks := strings.Repeat("`", longestRun(text, '`')+1)
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
		text = " " + text + " "
	}
	return ticks + text + ticks
}

// escapeText escapes characters that would start Markdown syntax inside a line.
func escapeText(text string) string {
	runes := []rune(text)
	var b strings.Builder
	for i, r := range runes {
		switch r {
		case '\\', '`', '*', '[', ']', '<':
			b.WriteRune('\\')
		case '_':
			if i == 0 || i == len(runes)-1 || !wordRune(runes[i-1]) || !wordRune(runes[i+1]) {
				b.WriteRune('\\')
			}
		case '~':
			if (i > 0 && runes[i-1] == '~') || (i < len(runes)-1 && runes[i+1] == '~') {
				b.WriteRune('\\')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// escapeLineStarts keeps lines of a paragraph from starting headings, quotes, or lists.
func escapeLineStarts(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		line = lineStartPattern.ReplaceAllString(line, `\$1`)
		lines[i] = numberedPattern.ReplaceAllString(line, `$1\$2`)
	}
	return strings.Join(lines, "\n")
}

func link(url string) string {
	if len(url) == 0 {
		return ""
	}

	return "[" + escapeText(url) + "](" + destination(url) + ")"
}

func destination(url string) string {
	return strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(url)
}

func formatDate(timestamp string) string {
	milliseconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return escapeText(timestamp)
	}

	return time.UnixMilli(milliseconds).UTC().Format(time.DateOnly)
}
