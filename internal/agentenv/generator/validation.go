package generator

import (
	"errors"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// instructionDocument provides rendered content and a shared Markdown tree to rules.
type instructionDocument struct {
	rendered *renderedTemplate
	markdown ast.Node
}

type instructionValidationRule struct {
	name  string
	check func(*instructionDocument) error
}

// validateInstructions runs the instruction rules before outputs are prepared.
func validateInstructions(rendered *renderedTemplate) error {
	markdown := goldmark.New(goldmark.WithExtensions(extension.Table))
	document := &instructionDocument{
		rendered: rendered,
		markdown: markdown.Parser().Parse(text.NewReader([]byte(rendered.content))),
	}

	rules := []instructionValidationRule{
		{
			name:  "headings",
			check: validateInstructionHeadings,
		},
	}

	var failures []error
	for _, rule := range rules {
		if err := rule.check(document); err != nil {
			failures = append(failures, fmt.Errorf("invalid instruction %s: %w", rule.name, err))
		}
	}

	return errors.Join(failures...)
}

// validateInstructionHeadings checks the document and each chunk's section boundary.
func validateInstructionHeadings(document *instructionDocument) error {
	previous, section, titles := 0, 0, 0
	seenChunks := make(map[int]bool)
	err := ast.Walk(document.markdown, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		heading, ok := node.(*ast.Heading)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}

		source := document.rendered.sources.sourceAt(heading.Pos())
		location := fmt.Sprintf("%s:%d", source.path, source.line)
		if heading.Level == 1 {
			titles++
			if titles > 1 {
				return ast.WalkStop, fmt.Errorf("%s: only one H1 document title is allowed", location)
			}
		}
		if source.chunk > 0 {
			base := section + 1
			if section == 0 {
				return ast.WalkStop, fmt.Errorf(
					"%s: chunk headings need a containing section in the main template",
					location,
				)
			}
			if !seenChunks[source.chunk] && heading.Level != base {
				return ast.WalkStop, fmt.Errorf(
					"%s: first chunk heading is H%d; expected H%d below the containing H%d section",
					location,
					heading.Level,
					base,
					section,
				)
			}
			if heading.Level < base {
				return ast.WalkStop, fmt.Errorf("%s: chunk heading H%d escapes the containing H%d section",
					location, heading.Level, section)
			}

			seenChunks[source.chunk] = true
		} else {
			section = heading.Level
			clear(seenChunks)
		}
		if heading.Level > previous+1 {
			return ast.WalkStop, fmt.Errorf("%s: heading skips from H%d to H%d; expected at most H%d",
				location, previous, heading.Level, previous+1)
		}

		previous = heading.Level
		return ast.WalkContinue, nil
	})
	if err != nil {
		return err
	}
	if titles == 0 {
		return fmt.Errorf("%s: rendered instructions must have one H1 document title", document.rendered.sources.main)
	}
	return nil
}
