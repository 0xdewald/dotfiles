package board

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Exec is everything the board runs outside the DB. Tests may swap parts.
type Exec interface {
	// Claude runs the claude binary in dir with extra env and returns stdout.
	Claude(dir string, env []string, args ...string) (string, error)
	// Git runs git in dir and returns trimmed stdout.
	Git(dir string, args ...string) (string, error)
	// Notify shows a desktop notification (best effort, non-blocking).
	Notify(title, msg string)
}

type sysExec struct{}

// DefaultExec runs real processes; the claude path comes from $CREW_CLAUDE.
func DefaultExec() Exec { return sysExec{} }

func claudeBin() string {
	if c := os.Getenv("CREW_CLAUDE"); c != "" {
		return c
	}
	return "claude"
}

func (sysExec) Claude(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command(claudeBin(), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("claude %s: %w: %s", strings.Join(args[:min(len(args), 2)], " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func (sysExec) Git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (sysExec) Notify(title, msg string) {
	if os.Getenv("CREW_NO_NOTIFY") == "1" {
		return
	}
	script := fmt.Sprintf("display notification %s with title %s", appleQuote(msg), appleQuote(title))
	cmd := exec.Command("osascript", "-e", script)
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

func appleQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ").Replace(s)
	return `"` + s + `"`
}

// claude --bg prints "backgrounded · <short id> · <name> …". It chooses the session id itself
// (it ignores --session-id), so this line is where crew learns it.
var backgroundedRe = regexp.MustCompile(`backgrounded\s*·\s*([0-9a-f]{8})\b`)

func parseBackgrounded(out string) string {
	if m := backgroundedRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// fullSessionID looks up the full session id for a background short id in `claude agents --json`.
func (b *Board) fullSessionID(dir, short string) (string, error) {
	for try := 0; try < 10; try++ {
		out, err := b.Exec.Claude(dir, nil, "agents", "--json", "--all")
		if err == nil {
			var list []struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal([]byte(out), &list) == nil {
				for _, s := range list {
					if strings.HasPrefix(s.SessionID, short) {
						return s.SessionID, nil
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("claude agents doesn't list background session %s", short)
}
