package mcpserver

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/doctor/check"
)

// DoctorCheck reads server definitions when it runs, allowing the caller to
// register a dependency on its configuration check before any connections start.
func DoctorCheck(root string, servers func() map[string]config.MCPServer, options DoctorOptions) check.Check {
	if options.Timeout == 0 {
		options.Timeout = 10 * time.Second
	}
	return check.Check{ID: "mcp.servers", Run: func(ctx context.Context) check.Result {
		configured := servers()
		var serverChecks []check.Check
		for _, name := range slices.Sorted(maps.Keys(configured)) {
			server := configured[name]
			if !server.Enabled {
				continue
			}
			serverChecks = append(serverChecks, mcpChecks(root, name, server, options)...)
		}
		if len(serverChecks) == 0 {
			return check.Result{Status: check.Skipped, Message: "No enabled MCP servers"}
		}
		report, err := check.Run(ctx, serverChecks...)
		if err != nil {
			return check.Result{Status: check.Fail, Message: "MCP checks interrupted"}
		}
		return check.Result{Status: report.Status, Message: "MCP server checks", Children: report.Checks}
	}}
}

func mcpChecks(root, name string, server config.MCPServer, options DoctorOptions) []check.Check {
	id := "mcp." + name
	cwd := server.CWD
	if cwd == "" {
		cwd = root
	} else if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(root, cwd)
	}
	return []check.Check{{ID: id + ".configuration", Run: func(ctx context.Context) check.Result {
		if server.Transport() != config.MCPServerTypeStdio {
			return check.Result{Status: check.Pass, Message: "Remote transport configured; connectivity unchecked"}
		}
		info, err := os.Stat(cwd)
		if err != nil || !info.IsDir() {
			return check.Result{
				Status:      check.Fail,
				Message:     "MCP working directory is unavailable",
				Remediation: "Correct the server cwd or prepare its repository",
			}
		}
		command := server.Command
		if strings.ContainsAny(command, "/\\") && !filepath.IsAbs(command) {
			command = filepath.Join(cwd, command)
		}
		if _, err := exec.LookPath(command); err != nil {
			return check.Result{
				Status:      check.Fail,
				Message:     "MCP executable is unavailable",
				Remediation: "Install or configure the server command",
			}
		}
		return check.Result{
			Status:  check.Pass,
			Message: "Executable and working directory are available; wrapper arguments unchecked",
		}
	}}, {ID: id + ".probe", DependsOn: []string{id + ".configuration"}, Run: func(ctx context.Context) check.Result {
		if !options.ProbeMCP {
			return check.Result{
				Status:      check.Skipped,
				Message:     "Connection probe was not requested",
				Remediation: "Use --probe-mcp to initialize servers and list tools",
			}
		}
		server.CWD = cwd
		return probeMCP(ctx, server, options.Timeout)
	}}}
}
