package board

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
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

var (
	bgIDLabeled = regexp.MustCompile(`(?i)\b(?:id|session|agent)\b[^A-Za-z0-9]{0,3}([A-Za-z0-9][A-Za-z0-9_-]{2,})`)
	bgIDToken   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{2,}$`)
)

// parseBgID pulls a background session id out of `claude --bg` output.
// The format isn't documented, so: a lone token, else "id: X"-style, else
// the last word of the output that looks like an id; "" if nothing fits.
func parseBgID(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	if bgIDToken.MatchString(out) && strings.ContainsAny(out, "0123456789") {
		return out
	}
	for _, m := range bgIDLabeled.FindAllStringSubmatch(out, -1) {
		if strings.ContainsAny(m[1], "0123456789") {
			return m[1]
		}
	}
	f := strings.Fields(out)
	for i := len(f) - 1; i >= 0; i-- {
		w := strings.Trim(f[i], ".,:;()[]'\"`")
		if bgIDToken.MatchString(w) && strings.ContainsAny(w, "0123456789") {
			return w
		}
	}
	return ""
}
