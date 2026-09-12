package provider

import "fmt"

// MCPToolName formats a raw server tool name for client permissions and prompts.
func MCPToolName(providerName, serverName, toolName string) string {
	switch providerName {
	case Claude:
		return fmt.Sprintf("mcp__%s__%s", serverName, toolName)
	case Crush, Gemini:
		return fmt.Sprintf("mcp_%s_%s", serverName, toolName)
	case Copilot:
		return serverName + "/" + toolName
	default:
		return toolName
	}
}
