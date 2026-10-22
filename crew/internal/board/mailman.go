package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The mailman delivers messages to an agent whose session is running, without restarting it.
// Resuming a session to give it a message starts a new Claude process, which reconnects every MCP
// connector (seen with real agents: connectors dropped between messages, and a turn started before
// they were back). Claude sessions on this machine can message each other through their session
// socket (<cc-socks>/<pid>.sock), so crew runs a short headless Claude (a small model, one tool call)
// that sends the message there. The agent's process keeps running.

var errNotRunning = errors.New("session not running")

// livePID is the process of the agent's running session, per `claude agents --json` (a live
// session has a status); 0 when it isn't running.
func (b *Board) livePID(a *Agent) int {
	out, err := b.Exec.Claude(a.Dir, nil, "agents", "--json", "--all")
	if err != nil {
		return 0
	}
	var list []struct {
		SessionID string  `json:"sessionId"`
		PID       int     `json:"pid"`
		Status    *string `json:"status"`
	}
	if json.Unmarshal([]byte(out), &list) != nil {
		return 0
	}
	for _, s := range list {
		if s.SessionID == a.SessionID && s.PID > 0 && s.Status != nil && *s.Status != "" {
			return s.PID
		}
	}
	return 0
}

// sessionSocket is where a Claude session listens for messages from other sessions.
func sessionSocket(pid int) string {
	dir := os.Getenv("CREW_CC_SOCKS")
	if dir == "" {
		dir = "/tmp/cc-socks"
	}
	return filepath.Join(dir, fmt.Sprintf("%d.sock", pid))
}

func mailmanModel() string {
	if m := os.Getenv("CREW_MAILMAN_MODEL"); m != "" {
		return m
	}
	return "haiku"
}

const mailmanPrompt = `You are crew's mail carrier. Use the SendMessage tool (load it with ToolSearch first if it isn't loaded) to send the message below, exactly as written and in full, to this recipient: %s

Send it once, then reply with exactly SENT. If you can't send it, reply with FAILED and the reason.

----- message -----
%s`

// mail hands text to the agent's running session. It fails (and the caller resumes instead) when
// the session isn't running or the mailman couldn't send it.
func (b *Board) mail(a *Agent, text string) error {
	pid := b.livePID(a)
	if pid == 0 {
		return errNotRunning
	}
	sock := sessionSocket(pid)
	if _, err := os.Stat(sock); err != nil {
		return fmt.Errorf("no session socket %s", sock)
	}
	framed := "Message from your human, sent through crew (the board that runs you):\n\n" + text
	out, err := b.Exec.ClaudeInput(a.Dir, nil, fmt.Sprintf(mailmanPrompt, "uds:"+sock, framed),
		"-p", "--model", mailmanModel(), "--permission-mode", permissionMode(a.Role),
		"--allowedTools", "SendMessage,ToolSearch")
	if err != nil {
		return err
	}
	if reply := strings.TrimSpace(out); !strings.HasPrefix(reply, "SENT") {
		return fmt.Errorf("mailman: %s", truncate(oneLine(reply), 200))
	}
	return nil
}

// SessionAddress is the agent's running session as a SendMessage recipient, "" if not running.
func (b *Board) SessionAddress(a *Agent) string {
	if pid := b.livePID(a); pid > 0 {
		if _, err := os.Stat(sessionSocket(pid)); err == nil {
			return "uds:" + sessionSocket(pid)
		}
	}
	return ""
}
