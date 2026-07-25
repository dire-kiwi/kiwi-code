package server

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

//go:embed pi-figma-mcp/extension.ts
var piFigmaMCPExtension []byte

const (
	// figmaMCPServerName is the MCP server name Claude Code sees. Keep it stable:
	// tool names the model learns are derived from it.
	figmaMCPServerName = "figma"
	// figmaMCPEnvironmentName carries the endpoint to the Pi bridge extension.
	figmaMCPEnvironmentName = "KIWI_CODE_FIGMA_MCP_URL"
)

func materializePiFigmaMCPExtension(dataDirectory string) (string, error) {
	return materializePiExtension(dataDirectory, "kiwi-code-figma-mcp.ts", piFigmaMCPExtension)
}

// figmaMCPConfigArgument builds the value for Claude Code's --mcp-config flag.
// Figma's Dev Mode server speaks streamable HTTP, so declare the http type
// rather than letting Claude guess from the URL.
func figmaMCPConfigArgument(url string) (string, error) {
	config := map[string]any{
		"mcpServers": map[string]any{
			figmaMCPServerName: map[string]any{
				"type": "http",
				"url":  url,
			},
		},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode Figma MCP config: %w", err)
	}
	return string(encoded), nil
}

// figmaMCPURLForProject returns the Figma Dev Mode endpoint coding agents should
// use for this project, or an empty string when the project has Figma MCP support
// disabled. The endpoint is Figma's local, unauthenticated Dev Mode server, so it
// is the same for every project and every agent.
func (h *terminalHandler) figmaMCPURLForProject(item project.Project) string {
	if !item.FigmaMCPEnabled {
		return ""
	}
	return project.DefaultFigmaMCPURL
}
