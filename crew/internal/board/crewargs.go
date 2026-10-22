package board

import (
	"bytes"
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

// Role instructions, appended to Claude's system prompt (prompts/<role>.md; roles without one get
// none). Like the MCP config they're not part of the conversation, so every start and resume
// passes them again.
//
//go:embed prompts
var prompts embed.FS

// executable is the crew binary agents' MCP server runs: the one running now.
var executable = os.Executable

// crewArgs gives an agent crew's MCP tools (ask_decision, report_done, …) through
// <home>/agents/<id>/mcp.json, rewritten each time so it names the current crew binary, and its
// role's instructions.
func (b *Board) crewArgs(a *Agent) ([]string, error) {
	file, err := b.writeMCPConfig(a)
	if err != nil {
		return nil, err
	}
	args := []string{"--mcp-config", file}
	if p := rolePrompt(a); p != "" {
		args = append(args, "--append-system-prompt", p)
	}
	return args, nil
}

// CrewArgs is crewArgs for clients that start the agent's session themselves (Neovim's take-over),
// so it has crew's tools and instructions there too.
func (b *Board) CrewArgs(a *Agent) ([]string, error) { return b.crewArgs(a) }

// MCPConfigPath is where an agent's MCP config lives.
func (b *Board) MCPConfigPath(id int64) string {
	return filepath.Join(b.Home, "agents", strconv.FormatInt(id, 10), "mcp.json")
}

func (b *Board) writeMCPConfig(a *Agent) (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return "", err
	}
	cfg := map[string]any{"mcpServers": map[string]any{"crew": map[string]any{
		"command": exe,
		"args":    []string{"mcp"},
		"env": map[string]string{
			"CREW_HOME":       b.Home,
			"CREW_AGENT_ID":   strconv.FormatInt(a.ID, 10),
			"CREW_AGENT_NAME": a.Name,
		},
	}}}
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	file := b.MCPConfigPath(a.ID)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	return file, os.WriteFile(file, append(raw, '\n'), 0o644)
}

func rolePrompt(a *Agent) string {
	raw, err := prompts.ReadFile("prompts/" + a.Role + ".md")
	if err != nil {
		return ""
	}
	t, err := template.New(a.Role).Parse(string(raw))
	if err != nil {
		return ""
	}
	branch := a.Branch
	if branch == "" {
		branch = "your branch"
	}
	var sb bytes.Buffer
	t.Execute(&sb, map[string]string{"Name": a.Name, "Branch": branch, "Dir": a.Dir})
	return strings.TrimSpace(sb.String())
}
