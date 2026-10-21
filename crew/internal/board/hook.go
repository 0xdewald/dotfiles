package board

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// HookPayload is the common shape of a Claude Code hook's stdin JSON.
type HookPayload struct {
	Event          string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	Message        string `json:"message"`                // Notification
	LastAssistant  string `json:"last_assistant_message"` // Stop, in Claude Code versions that send it
}

// HandleHook applies a hook payload. It returns false (and no error) when the
// session isn't a crew agent.
func (b *Board) HandleHook(raw []byte) (bool, error) {
	var p HookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return false, err
	}
	if p.SessionID == "" {
		return false, nil
	}
	a, err := b.AgentBySession(p.SessionID)
	if errors.Is(err, ErrNotFound) {
		// A session crew doesn't know yet, started for one of its agents (claude passes its env to
		// hooks, and crew starts agents with CREW_AGENT_ID): a fresh background start, a resume
		// that became a copy, /clear, or the agent opened in Neovim. Follow it to the new session.
		a, err = b.followSession(p.SessionID)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
	}
	if err != nil {
		return false, err
	}
	data := map[string]any{"hook": p.Event}
	switch p.Event {
	case "SessionStart":
		if a.Status == StatusStarting || a.Status == StatusStopped || a.Status == StatusStale {
			err = b.setStatus(a.ID, StatusIdle, nil) // ready and waiting for a prompt
		}
	case "UserPromptSubmit":
		err = b.setStatus(a.ID, StatusWorking, nil)
	case "Stop":
		last := a.LastMessage
		if p.LastAssistant != "" {
			last = p.LastAssistant
		} else if t, terr := settledLastText(p.TranscriptPath); terr == nil && t != "" {
			last = t
		}
		if err = b.setStatus(a.ID, StatusIdle, &last); err != nil {
			break
		}
		data["last_message"] = truncate(last, 500)
		n, derr := b.deliver(a.ID)
		if derr != nil {
			data["deliver_error"] = derr.Error()
		}
		data["delivered"] = n
		if n == 0 {
			b.Exec.Notify("crew: "+a.Name+" is done", truncate(oneLine(last), 120))
		}
	case "Notification":
		if err = b.setStatus(a.ID, StatusNeedsInput, nil); err != nil {
			break
		}
		data["message"] = p.Message
		msg := p.Message
		if msg == "" {
			msg = "needs your input"
		}
		b.Exec.Notify("crew: "+a.Name+" needs input", truncate(oneLine(msg), 120))
	}
	if eerr := b.Event("hook."+strings.ToLower(p.Event), &a.ID, a.ItemID, data); err == nil {
		err = eerr
	}
	return true, err
}

// followSession moves the agent named by $CREW_AGENT_ID onto session sid.
func (b *Board) followSession(sid string) (*Agent, error) {
	id, err := strconv.ParseInt(os.Getenv("CREW_AGENT_ID"), 10, 64)
	if err != nil {
		return nil, ErrNotFound
	}
	a, err := b.AgentByID(id)
	if err != nil {
		return nil, err
	}
	if _, err := b.DB.Exec(`UPDATE agents SET session_id = ?, updated = ? WHERE id = ?`, sid, b.now(), id); err != nil {
		return nil, err
	}
	b.Event("agent.session", &id, a.ItemID, map[string]any{"from": a.SessionID, "to": sid})
	return b.AgentByID(id)
}

// settledLastText reads the last assistant text once the transcript stops growing. Claude runs
// the Stop hook a moment before it finishes writing the turn's final message to the file.
func settledLastText(path string) (string, error) {
	size, stable := int64(-1), 0
	for i := 0; i < 30 && stable < 3; i++ { // unchanged for 300ms, at most ~3s
		st, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if st.Size() == size {
			stable++
		} else {
			size, stable = st.Size(), 0
		}
		time.Sleep(100 * time.Millisecond)
	}
	return LastAssistantText(path)
}

// LastAssistantText returns the text of the last assistant message in a
// Claude Code JSONL transcript.
func LastAssistantText(path string) (string, error) {
	if path == "" {
		return "", errors.New("no transcript")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	const tail = 8 << 20 // only the end matters
	if st, err := f.Stat(); err == nil && st.Size() > tail {
		f.Seek(st.Size()-tail, io.SeekStart)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	last := ""
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"assistant"`) {
			continue
		}
		var e struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil || e.Type != "assistant" {
			continue
		}
		if t := contentText(e.Message.Content); t != "" {
			last = t
		}
	}
	return last, sc.Err()
}

func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &parts)
	var out []string
	for _, p := range parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			out = append(out, strings.TrimSpace(p.Text))
		}
	}
	return strings.Join(out, "\n")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
