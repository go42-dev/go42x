package html2md

import (
	"bytes"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
)

const HTMLToMarkdown = "html-to-markdown"

type HTMLToMarkdownConverter struct {
	converter *converter.Converter
}

func NewHTMLToMarkdownConverter() *HTMLToMarkdownConverter {
	return &HTMLToMarkdownConverter{
		converter: converter.NewConverter(
			converter.WithPlugins(
				base.NewBasePlugin(),
				commonmark.NewCommonmarkPlugin(),
				strikethrough.NewStrikethroughPlugin(),
				// Keep tables whose cells contain line breaks, and pad cells minimally so
				// one changed cell does not rewrite every line of a vendored table.
				table.NewTablePlugin(
					table.WithNewlineBehavior(table.NewlineBehaviorPreserve),
					table.WithCellPaddingBehavior(table.CellPaddingBehaviorMinimal),
				),
			),
		),
	}
}

// Convert converts the complete document, including tables and strikethrough, while
// preserving link targets. Line breaks inside table cells become <br /> tags.
func (c *HTMLToMarkdownConverter) Convert(data []byte) ([]byte, error) {
	markdown, err := c.converter.ConvertReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	return append(bytes.TrimSpace(markdown), '\n'), nil
}
