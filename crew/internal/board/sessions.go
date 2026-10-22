package board

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// claudeHome is Claude Code's data folder: $CLAUDE_CONFIG_DIR or ~/.claude.
func claudeHome() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// Claude keeps a folder's conversations in <claude home>/projects/<path, non-alphanumerics as "-">.
func projectDir(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	enc := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, dir)
	return filepath.Join(claudeHome(), "projects", enc)
}

func hasConversation(dir, sid string) bool {
	if sid == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(projectDir(dir), sid+".jsonl"))
	return err == nil
}

// ConversationID is the session holding the agent's conversation: its recorded session if that
// has a conversation file, else the newest conversation in its folder named after the agent (crew
// names sessions with -n, and Claude records that name in the file). false if it has none yet.
func (b *Board) ConversationID(a *Agent) (string, bool) {
	pdir := projectDir(a.Dir)
	// a newer conversation with the agent's name wins: /clear starts one, and the board may not
	// have seen it yet
	var recorded time.Time
	if st, err := os.Stat(filepath.Join(pdir, a.SessionID+".jsonl")); err == nil && a.SessionID != "" {
		recorded = st.ModTime()
	}
	entries, err := os.ReadDir(pdir)
	if err != nil {
		return "", false
	}
	needle := `"customTitle":` + jsonString(a.Name)
	best, bestTime := "", time.Time{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || (best != "" && !info.ModTime().After(bestTime)) {
			continue
		}
		if fileContains(filepath.Join(pdir, e.Name()), needle) {
			best, bestTime = strings.TrimSuffix(e.Name(), ".jsonl"), info.ModTime()
		}
	}
	if !recorded.IsZero() && (best == "" || !bestTime.After(recorded)) {
		return a.SessionID, true
	}
	return best, best != ""
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func fileContains(path, needle string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if strings.Contains(sc.Text(), needle) {
			return true
		}
	}
	return false
}

// runBackground runs the agent in the background: resuming its conversation when it has one,
// else starting it fresh, with text as the next prompt (may be empty). Returns the session id
// and short background id once Claude confirms the session is running.
func (b *Board) runBackground(a *Agent, text string) (string, string, error) {
	crew, err := b.crewArgs(a)
	if err != nil {
		return "", "", err
	}
	args := startArgs(a.Name, crew, permissionMode(a.Role), a.Model, text)
	if sid, ok := b.ConversationID(a); ok {
		// the MCP config and instructions aren't part of the conversation: give them again
		args = append(append([]string{"--bg", "--resume", sid}, crew...), "--permission-mode", permissionMode(a.Role))
		if text != "" {
			args = append(args, text)
		}
	}
	launched := time.Now()
	out, err := b.Exec.Claude(a.Dir, b.agentEnv(a), args...)
	if err != nil {
		return "", "", err
	}
	bg := parseBackgrounded(out)
	if bg == "" {
		return "", "", fmt.Errorf("claude --bg: no session id in its output: %s", strings.TrimSpace(out))
	}
	sid, err := b.awaitBackground(a.Dir, bg, launched)
	return sid, bg, err
}

// awaitBackground waits until `claude agents` shows background session short as running and
// returns its full session id. claude --bg returns before the session starts, and a session that
// can't start (e.g. nothing to resume) only shows up as failed a moment later.
func (b *Board) awaitBackground(dir, short string, launched time.Time) (string, error) {
	var sid string
	for try := 0; try < 25; try++ { // ~5s
		out, err := b.Exec.Claude(dir, nil, "agents", "--json", "--all")
		if err == nil {
			var list []struct {
				SessionID string  `json:"sessionId"`
				Status    *string `json:"status"`
			}
			if json.Unmarshal([]byte(out), &list) == nil {
				for _, s := range list {
					if strings.HasPrefix(s.SessionID, short) {
						sid = s.SessionID
						if s.Status != nil && *s.Status != "" {
							return sid, nil
						}
					}
				}
			}
		}
		if detail, failed := jobFailed(short, launched); failed {
			return "", fmt.Errorf("Claude couldn't start background session %s: %s", short, detail)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if sid == "" {
		return "", fmt.Errorf("claude agents doesn't list background session %s", short)
	}
	return sid, nil // listed but not reporting a status yet: assume it's starting
}

// jobFailed reads Claude Code's record of a background job (<claude home>/jobs/<short>/state.json).
// A resumed session reuses its short id, so a record older than this launch is an earlier run's.
func jobFailed(short string, launched time.Time) (string, bool) {
	path := filepath.Join(claudeHome(), "jobs", short, "state.json")
	if st, err := os.Stat(path); err != nil || st.ModTime().Before(launched.Add(-time.Second)) {
		return "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var st struct {
		State  string `json:"state"`
		Detail any    `json:"detail"`
	}
	if json.Unmarshal(raw, &st) != nil || st.State != "failed" {
		return "", false
	}
	return strings.TrimSpace(fmt.Sprint(st.Detail)), true
}
