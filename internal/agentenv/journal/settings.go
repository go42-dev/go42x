package journal

import (
	"fmt"
	"strings"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/antigravity"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/codex"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/copilot"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/crush"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
)

type Settings struct {
	RootPath string
	Actor    Actor
	JSON     bool
}

func NewSettings() *Settings {
	return &Settings{
		RootPath: ".",
	}
}

func (s *Settings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings are required")
	}
	if len(strings.TrimSpace(s.RootPath)) == 0 {
		return fmt.Errorf("project root is required")
	}
	if len(s.Actor.Runtime) > 0 {
		if err := s.Actor.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// DetectActor identifies the runtime and session of a CLI call. Supplied values, such as
// the --runtime and --session flags, win. Runtime markers in the environment follow, then
// a terminal on standard input, which indicates a person. Without a supplied session, the
// session is the one SessionFromEnv finds for the runtime.
func DetectActor(runtime, session string, lookup func(string) (string, bool), terminal bool) Actor {
	if len(runtime) == 0 {
		if agent, found := lookup("AI_AGENT"); found && len(agent) > 0 {
			name, _, _ := strings.Cut(agent, "_")
			runtime = RuntimeFromClient(name)
		}
	}
	if len(runtime) == 0 || runtime == RuntimeUnknown {
		if value, _ := lookup("CLAUDECODE"); value == "1" {
			runtime = claude.Name
		}
	}
	if len(runtime) == 0 || runtime == RuntimeUnknown {
		runtime = RuntimeUnknown
		if terminal {
			runtime = RuntimeHuman
		}
	}

	if len(session) == 0 {
		session = SessionFromEnv(runtime, lookup)
	}

	return Actor{
		Runtime: runtime,
		Session: session,
	}
}

// SessionFromEnv returns the session that a runtime names in the environment of the
// processes it starts, for commands and MCP servers alike, or an empty string when it
// names none. A runtime gets a case once the variable that names its session is known.
func SessionFromEnv(runtime string, lookup func(string) (string, bool)) string {
	switch runtime {
	case claude.Name:
		session, _ := lookup("CLAUDE_CODE_SESSION_ID")
		return session
	default:
		return ""
	}
}

// RuntimeFromClient maps a client or agent name, such as an MCP client's name, to the
// runtime an entry records.
func RuntimeFromClient(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "claude"):
		return claude.Name
	case strings.Contains(name, "codex"):
		return codex.Name
	case strings.Contains(name, "copilot"):
		return copilot.Name
	case strings.Contains(name, "antigravity"), strings.Contains(name, "gemini"):
		return antigravity.Name
	case strings.Contains(name, "crush"):
		return crush.Name
	case strings.Contains(name, ghaw.Name):
		return ghaw.Name
	default:
		return RuntimeUnknown
	}
}
