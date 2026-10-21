package copilot

import (
	"log/slog"
	"testing"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
)

func TestEmptyTools(t *testing.T) {
	p := New(
		slog.New(slog.DiscardHandler),
		nil,
		Settings{MCPServers: map[string]provider.MCPServer{"empty": {
			Enabled: true,
			Command: "example",
		}}},
		"",
	)

	if p.extractMCPServers()["empty"].Tools == nil {
		t.Error("Name requires a tools array, even when empty")
	}
}
