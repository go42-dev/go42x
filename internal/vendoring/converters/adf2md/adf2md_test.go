package adf2md

import (
	"testing"
)

func paragraph(content string) string {
	return `{"type": "paragraph", "content": [` + content + `]}`
}

func text(value string) string {
	return `{"type": "text", "text": "` + value + `"}`
}

func TestConvert(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "marks",
			content: paragraph(`
				{"type": "text", "text": "Use "},
				{"type": "text", "text": "bold ", "marks": [{"type": "strong"}]},
				{"type": "text", "text": "text", "marks": [{"type": "strong"}]},
				{"type": "text", "text": ", "},
				{"type": "text", "text": "it", "marks": [{"type": "em"}]},
				{"type": "text", "text": ", "},
				{"type": "text", "text": "a` + "`" + `b", "marks": [{"type": "code"}]},
				{"type": "text", "text": ", "},
				{"type": "text", "text": "old", "marks": [{"type": "strike"}]},
				{"type": "text", "text": " and "},
				{"type": "text", "text": "docs", "marks": [{"type": "link", "attrs": {"href": "https://example.com/a b"}}]},
				{"type": "text", "text": "."}`),
			want: "Use **bold text**, *it*, ``a`b``, ~~old~~ and [docs](https://example.com/a%20b).",
		},
		{
			name: "escaping",
			content: paragraph(text("# not a heading * star _x_ snake_case <tag> ~~no~~")) + "," +
				paragraph(text("1. not a list")),
			want: `\# not a heading \* star \_x\_ snake_case \<tag> \~\~no\~\~` + "\n\n" + `1\. not a list`,
		},
		{
			name: "headings",
			content: `{"type": "heading", "attrs": {"level": 2}, "content": [` + text("Install") + `]},
				{"type": "heading", "attrs": {"level": 3}, "content": []}`,
			want: "## Install",
		},
		{
			name: "nested lists",
			content: `{"type": "orderedList", "attrs": {"order": 3}, "content": [
				{"type": "listItem", "content": [` + paragraph(text("Three")) + `]},
				{"type": "listItem", "content": [` + paragraph(text("Four")) + `,
					{"type": "bulletList", "content": [
						{"type": "listItem", "content": [` + paragraph(text("a")) + `]},
						{"type": "listItem", "content": [` + paragraph(text("b")) + `]}
					]}
				]}
			]}`,
			want: "3. Three\n4. Four\n   - a\n   - b",
		},
		{
			name: "tasks and decisions",
			content: `{"type": "taskList", "content": [
				{"type": "taskItem", "attrs": {"state": "DONE"}, "content": [` + text("Done") + `]},
				{"type": "taskItem", "attrs": {"state": "TODO"}, "content": [` + text("Open") + `]},
				{"type": "taskList", "content": [
					{"type": "taskItem", "attrs": {"state": "TODO"}, "content": [` + text("Child") + `]}
				]}
			]},
			{"type": "decisionList", "content": [
				{"type": "decisionItem", "attrs": {"state": "DECIDED"}, "content": [` + text("Use ADF") + `]}
			]}`,
			want: "- [x] Done\n- [ ] Open\n  - [ ] Child\n\n- ✓ Use ADF",
		},
		{
			name: "code",
			content: `{"type": "codeBlock", "attrs": {"language": "go"}, "content": [` +
				text(`fmt.Println(\"`+"```"+`\")`) + `]},
				{"type": "codeBlock", "attrs": {"language": "go lang"}, "content": [` + text("x") + `]}`,
			want: "````go\nfmt.Println(\"```\")\n````\n\n```\nx\n```",
		},
		{
			name: "quotes and panels",
			content: `{"type": "blockquote", "content": [` + paragraph(text("Quoted")) + `]},
				{"type": "panel", "attrs": {"panelType": "warning"}, "content": [` + paragraph(text("Careful")) + `]},
				{"type": "panel", "attrs": {"panelType": "custom"}, "content": [` + paragraph(text("Plain")) + `]}`,
			want: "> Quoted\n\n> [!WARNING]\n> Careful\n\n> [!NOTE]\n> Plain",
		},
		{
			name:    "expand",
			content: `{"type": "expand", "attrs": {"title": "A <b>"}, "content": [` + paragraph(text("Body")) + `]}`,
			want:    "<details>\n<summary>A &lt;b&gt;</summary>\n\nBody\n\n</details>",
		},
		{
			name: "table with header",
			content: `{"type": "table", "content": [
				{"type": "tableRow", "content": [
					{"type": "tableHeader", "content": [` + paragraph(text("H1")) + `]},
					{"type": "tableHeader", "content": [` + paragraph(text("H2")) + `]}
				]},
				{"type": "tableRow", "content": [
					{"type": "tableCell", "content": [` + paragraph(text("1")) + `]},
					{"type": "tableCell", "content": [` + paragraph(text("2")) + `]}
				]}
			]}`,
			want: "| H1 | H2 |\n| --- | --- |\n| 1 | 2 |",
		},
		{
			name: "table without header",
			content: `{"type": "table", "content": [
				{"type": "tableRow", "content": [
					{"type": "tableCell", "attrs": {"colspan": 2}, "content": [` + paragraph(text("a|b")) + `]}
				]},
				{"type": "tableRow", "content": [
					{"type": "tableCell", "content": [` + paragraph(text("c")) + `]},
					{"type": "tableCell", "content": [` + paragraph(text("d")) + "," + paragraph(text("e")) + `]}
				]}
			]}`,
			want: "|  |  |\n| --- | --- |\n| a\\|b |  |\n| c | d<br>e |",
		},
		{
			name: "media",
			content: `{"type": "mediaSingle", "content": [
				{"type": "media", "attrs": {"type": "external", "url": "https://example.com/i.png", "alt": "Diagram"}},
				{"type": "caption", "content": [` + text("Figure") + `]}
			]},
			{"type": "mediaGroup", "content": [{"type": "media", "attrs": {"type": "file", "id": "1"}}]}`,
			want: "![Diagram](https://example.com/i.png)\n\n*Figure*\n\n*[attachment]*",
		},
		{
			name: "macros",
			content: `{"type": "extension", "attrs": {"extensionKey": "toc"}},
				{"type": "bodiedExtension", "attrs": {"extensionKey": "details"}, "content": [` +
				paragraph(text("Inside")) + `]}`,
			want: "<!-- Confluence macro \"toc\" is not rendered -->\n\nInside",
		},
		{
			name: "inline nodes",
			content: paragraph(text("Hi ") + `,
				{"type": "mention", "attrs": {"id": "1", "text": "@Ann"}},
				` + text(" ") + `,
				{"type": "emoji", "attrs": {"shortName": ":grinning:", "text": "😀"}},
				` + text(" on ") + `,
				{"type": "date", "attrs": {"timestamp": "1700000000000"}},
				{"type": "hardBreak"},
				{"type": "status", "attrs": {"text": "IN PROGRESS", "color": "blue"}},
				` + text(" ") + `,
				{"type": "inlineCard", "attrs": {"url": "https://example.com/x"}}`),
			want: "Hi @Ann 😀 on 2023-11-14\\\n**IN PROGRESS** [https://example.com/x](https://example.com/x)",
		},
		{
			name: "layout and rule",
			content: `{"type": "layoutSection", "content": [
				{"type": "layoutColumn", "content": [` + paragraph(text("Left")) + `]},
				{"type": "layoutColumn", "content": [` + paragraph(text("Right")) + `]}
			]},
			{"type": "rule"}`,
			want: "Left\n\nRight\n\n---",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			document := `{"type": "doc", "version": 1, "content": [` + tc.content + `]}`
			got, err := NewADFToMarkdownConverter().Convert([]byte(document))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want+"\n" {
				t.Errorf("Convert =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestConvertRejectsNonDocuments(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"<html>", `{"type": "paragraph"}`} {
		if _, err := NewADFToMarkdownConverter().Convert([]byte(input)); err == nil {
			t.Errorf("Convert(%q) accepted a non-document", input)
		}
	}
}
