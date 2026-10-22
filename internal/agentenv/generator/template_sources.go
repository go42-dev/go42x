package generator

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"text/template/parse"
)

const sourceTrackingFunction = "__go42xSource"

type templateLocation struct {
	path  string
	line  int
	chunk int
}

type templateSpan struct {
	template instructionTemplate
	start    int
	end      int
	offset   int
	chunk    int
}

type renderedSource struct {
	output  int
	input   int
	literal bool
}

// templateSources maps rendered output back to the main template and its chunks.
type templateSources struct {
	main    string
	content string
	spans   []templateSpan
	marks   []renderedSource
}

func newTemplateSources(main instructionTemplate, chunks []instructionTemplate) *templateSources {
	sources := &templateSources{main: main.path}
	index := strings.Index(main.content, chunksPlaceholder)
	if index < 0 {
		sources.append(main, main.content, 0, 0)
		return sources
	}

	sources.append(main, main.content[:index], 0, 0)
	for i, chunk := range chunks {
		if i > 0 {
			sources.append(main, "\n\n", index, 0)
		}

		content := strings.TrimSpace(chunk.content)
		sources.append(chunk, content, strings.Index(chunk.content, content), i+1)
	}
	end := index + len(chunksPlaceholder)
	sources.append(main, main.content[end:], end, 0)
	return sources
}

func (s *templateSources) append(source instructionTemplate, content string, offset, chunk int) {
	if len(content) == 0 {
		return
	}

	start := len(s.content)
	s.content += content
	s.spans = append(s.spans, templateSpan{
		template: source,
		start:    start,
		end:      len(s.content),
		offset:   offset,
		chunk:    chunk,
	})
}

func (s *templateSources) spanAt(position int) templateSpan {
	for _, span := range s.spans {
		if position >= span.start && position < span.end {
			return span
		}
	}
	return templateSpan{}
}

// track inserts empty-output actions after parsing, preserving template whitespace
// trimming, shared variables, and control flow across chunk boundaries. Named
// templates retain the source of their call site, including yamlBlock helpers.
func (s *templateSources) track(tmpl *template.Template, output *bytes.Buffer) error {
	mark := func(position int, literal bool) string {
		s.marks = append(s.marks, renderedSource{
			output:  output.Len(),
			input:   position,
			literal: literal,
		})
		return ""
	}
	tmpl.Funcs(template.FuncMap{sourceTrackingFunction: mark})
	return s.trackList(tmpl.Root, mark)
}

func (s *templateSources) trackList(list *parse.ListNode, mark func(int, bool) string) error {
	if list == nil {
		return nil
	}

	nodes := make([]parse.Node, 0, len(list.Nodes))
	for _, node := range list.Nodes {
		switch n := node.(type) {
		case *parse.TextNode:
			position := int(n.Position())
			remaining := n.Text
			for len(remaining) > 0 {
				span := s.spanAt(position)
				length := min(len(remaining), span.end-position)
				piece := n.Copy().(*parse.TextNode)
				piece.Pos = parse.Pos(position)
				piece.Text = remaining[:length]
				marker, err := sourceMarker(position, true, mark)
				if err != nil {
					return err
				}

				nodes = append(nodes, marker, piece)
				position += length
				remaining = remaining[length:]
			}
			continue
		case *parse.ActionNode, *parse.TemplateNode:
			marker, err := sourceMarker(int(n.Position()), false, mark)
			if err != nil {
				return err
			}

			nodes = append(nodes, marker)
		case *parse.IfNode:
			if err := s.trackBranches(&n.BranchNode, mark); err != nil {
				return err
			}
		case *parse.RangeNode:
			if err := s.trackBranches(&n.BranchNode, mark); err != nil {
				return err
			}
		case *parse.WithNode:
			if err := s.trackBranches(&n.BranchNode, mark); err != nil {
				return err
			}
		}
		nodes = append(nodes, node)
	}
	list.Nodes = nodes
	return nil
}

func (s *templateSources) trackBranches(branch *parse.BranchNode, mark func(int, bool) string) error {
	if err := s.trackList(branch.List, mark); err != nil {
		return err
	}
	return s.trackList(branch.ElseList, mark)
}

func sourceMarker(position int, literal bool, mark func(int, bool) string) (parse.Node, error) {
	marker := template.New("source").Funcs(template.FuncMap{sourceTrackingFunction: mark})
	if _, err := marker.Parse(fmt.Sprintf("{{%s %d %t}}", sourceTrackingFunction, position, literal)); err != nil {
		return nil, err
	}
	return marker.Root.Nodes[0], nil
}

func (s *templateSources) sourceAt(position int) templateLocation {
	var source renderedSource
	for _, mark := range s.marks {
		if mark.output > position {
			break
		}

		source = mark
	}
	input := source.input
	if source.literal {
		input += position - source.output
	}
	span := s.spanAt(input)
	offset := min(len(span.template.content), max(0, span.offset+input-span.start))
	return templateLocation{
		path:  span.template.path,
		line:  1 + strings.Count(span.template.content[:offset], "\n"),
		chunk: span.chunk,
	}
}
