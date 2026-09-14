package mcpserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/doctor/check"
)

var doctorEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Probe responses and subprocess logs are intentionally not echoed: servers can
// include authentication values in errors. Only locally defined messages escape.
func probeMCP(parent context.Context, server config.MCPServer, timeout time.Duration) check.Result {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var missing []string
	unsupportedReference := false
	expand := func(value string) string {
		return os.Expand(value, func(name string) string {
			if !doctorEnvName.MatchString(name) {
				unsupportedReference = true
				return ""
			}
			value, ok := os.LookupEnv(name)
			if !ok || value == "" {
				missing = append(missing, name)
			}
			return value
		})
	}
	env := []string{}
	for _, key := range slices.Sorted(maps.Keys(server.Env)) {
		env = append(env, key+"="+expand(server.Env[key]))
	}
	headers := map[string]string{}
	for key, value := range server.Headers {
		headers[key] = expand(value)
	}
	url := expand(server.URL)
	if unsupportedReference {
		return check.Result{
			Status:  check.Skipped,
			Message: "Unsupported environment reference syntax; use $NAME or ${NAME}",
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return check.Result{
			Status:      check.Skipped,
			Message:     "Required environment variables are unavailable",
			Evidence:    slices.Compact(missing),
			Remediation: "Set the listed environment variables before probing",
		}
	}
	var c *client.Client
	var err error
	switch server.Transport() {
	case config.MCPServerTypeStdio:
		command := server.Command
		if strings.ContainsAny(command, "/\\") && !filepath.IsAbs(command) {
			command = filepath.Join(server.CWD, command)
		}
		t := transport.NewStdioWithOptions(command, env, server.Args,
			transport.WithCommandLogger(slog.New(slog.DiscardHandler)),
			transport.WithCommandStderrWriter(io.Discard),
			transport.WithCommandFunc(func(ctx context.Context, command string, env, args []string) (*exec.Cmd, error) {
				// #nosec G204 -- --probe-mcp explicitly runs the server selected by trusted local configuration, without a shell.
				cmd := exec.CommandContext(ctx, command, args...)
				cmd.Dir = server.CWD
				cmd.Env = append(os.Environ(), env...)
				cmd.WaitDelay = time.Second
				return cmd, nil
			}))
		c = client.NewClient(t)
	case config.MCPServerTypeHTTP:
		c, err = client.NewStreamableHttpClient(
			url,
			transport.WithHTTPHeaders(headers),
			transport.WithHTTPBasicClient(&http.Client{Timeout: timeout}),
		)
	case config.MCPServerTypeSSE:
		c, err = client.NewSSEMCPClient(
			url,
			client.WithHeaders(headers),
			client.WithHTTPClient(&http.Client{Timeout: timeout}),
		)
	}
	failure := func() check.Result {
		message := "MCP connection, initialization, or tool listing failed"
		if ctx.Err() != nil {
			message = "MCP probe timed out or was canceled"
		}
		return check.Result{
			Status:      check.Fail,
			Message:     message,
			Remediation: "Check the server command, arguments, credentials, and connectivity",
		}
	}
	if err != nil || c == nil {
		return failure()
	}
	defer func() { cancel(); _ = c.Close() }()
	if err = c.Start(ctx); err != nil {
		return failure()
	}
	request := mcp.InitializeRequest{}
	request.Params.ClientInfo = mcp.Implementation{Name: "go42x-doctor", Version: "1"}
	if _, err = c.Initialize(ctx, request); err != nil {
		return failure()
	}
	advertised := map[string]bool{}
	seenCursors := map[mcp.Cursor]bool{}
	requestTools := mcp.ListToolsRequest{}
	for page := 0; page < 100; page++ {
		tools, err := c.ListToolsByPage(ctx, requestTools)
		if err != nil {
			return failure()
		}
		for _, tool := range tools.Tools {
			advertised[tool.Name] = true
		}
		if tools.NextCursor == "" {
			break
		}
		if seenCursors[tools.NextCursor] || page == 99 {
			return check.Result{Status: check.Fail, Message: "MCP tool listing exceeded pagination limits"}
		}
		seenCursors[tools.NextCursor] = true
		requestTools.Params.Cursor = tools.NextCursor
	}
	var absent []string
	for _, name := range server.Tools {
		if !advertised[name] {
			absent = append(absent, name)
		}
	}
	if len(absent) > 0 {
		return check.Result{
			Status:      check.Fail,
			Message:     "Configured tools were not advertised",
			Evidence:    absent,
			Remediation: "Update configured tool names or server version",
		}
	}
	return check.Result{
		Status:  check.Pass,
		Message: fmt.Sprintf("Initialized server; %d tools advertised, all configured names present", len(advertised)),
	}
}
