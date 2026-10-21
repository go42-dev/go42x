package fspatch

import (
	"bytes"
	"fmt"
)

const (
	managedBlockStart = "# go42x -- START"
	managedBlockEnd   = "# go42x -- END"
)

// WriteManagedBlock schedules a replacement of the single go42x-managed block.
// Empty files receive the full template. Other files preserve all text outside
// the markers, appending the bundled block if no markers exist. Malformed or
// repeated markers in either input fail before a write is scheduled.
func (p *Plan) WriteManagedBlock(path, template string, kind Kind) error {
	bundled := []byte(template)
	blockStart, blockEnd, err := managedBlockRange(bundled)
	if err != nil {
		return fmt.Errorf("prepare %s template: %w", path, err)
	}
	if blockStart < 0 {
		return fmt.Errorf("prepare %s template: missing go42x markers", path)
	}

	previous, _, err := p.Read(path)
	if err != nil {
		return fmt.Errorf("prepare %s: %w", path, err)
	}

	start, end, err := managedBlockRange(previous)
	if err != nil {
		return fmt.Errorf("prepare %s: %w", path, err)
	}

	var content []byte
	block := bundled[blockStart:blockEnd]
	switch {
	case len(previous) == 0:
		content = bundled
	case start < 0:
		content = bytes.Clone(previous)
		if content[len(content)-1] != '\n' {
			content = append(content, '\n')
		}
		content = append(content, '\n')
		content = append(content, block...)
	default:
		content = append(content, previous[:start]...)
		content = append(content, block...)
		content = append(content, previous[end:]...)
	}

	return p.Write(path, content, kind, false)
}

func managedBlockRange(content []byte) (int, int, error) {
	start, end, offset := -1, -1, 0
	for line := range bytes.SplitAfterSeq(content, []byte("\n")) {
		switch string(bytes.TrimSpace(line)) {
		case managedBlockStart:
			if start != -1 || end != -1 {
				return -1, -1, fmt.Errorf("must contain a single matching pair of go42x markers")
			}
			start = offset
		case managedBlockEnd:
			if start == -1 || end != -1 {
				return -1, -1, fmt.Errorf("must contain a single matching pair of go42x markers")
			}
			end = offset + len(line)
		}
		offset += len(line)
	}
	if start != -1 && end == -1 {
		return -1, -1, fmt.Errorf("must contain a single matching pair of go42x markers")
	}

	return start, end, nil
}
