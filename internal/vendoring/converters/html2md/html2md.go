package html2md

import (
	"bytes"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

const HTMLToMarkdown = "html-to-markdown"

type HTMLToMarkdownConverter struct{}

func NewHTMLToMarkdownConverter() *HTMLToMarkdownConverter {
	return &HTMLToMarkdownConverter{}
}

// Convert converts the complete document while preserving link targets.
func (c *HTMLToMarkdownConverter) Convert(data []byte) ([]byte, error) {
	markdown, err := htmltomarkdown.ConvertReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	return append(bytes.TrimSpace(markdown), '\n'), nil
}
