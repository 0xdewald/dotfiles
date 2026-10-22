package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

// crew serve records itself in <home>/serve.json while it runs, so `crew status` can say whether
// a server is up, where, and whether it runs an older build than this crew (restart it then).

type serverInfo struct {
	PID     int    `json:"pid"`
	Addr    string `json:"addr"`
	Started string `json:"started"`
	Build   string `json:"build"`
}

// ServerState is what `crew status` reports about crew serve.
type ServerState struct {
	Running  bool   `json:"running"`
	PID      int    `json:"pid,omitempty"`
	Addr     string `json:"addr,omitempty"`
	Started  string `json:"started,omitempty"`
	Build    string `json:"build,omitempty"`
	Outdated bool   `json:"outdated,omitempty"` // runs an older build than this crew
}

// build is the git commit this binary was built from (go build embeds it), "dev" if unknown.
func build() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

func serverFile(home string) string { return filepath.Join(home, "serve.json") }

func writeServerInfo(home, addr string) error {
	raw, _ := json.Marshal(serverInfo{PID: os.Getpid(), Addr: addr, Started: time.Now().UTC().Format(time.RFC3339), Build: build()})
	return os.WriteFile(serverFile(home), raw, 0o644)
}

func removeServerInfo(home string) {
	// only our own record: another server may have replaced it
	if raw, err := os.ReadFile(serverFile(home)); err == nil {
		var info serverInfo
		if json.Unmarshal(raw, &info) == nil && info.PID == os.Getpid() {
			os.Remove(serverFile(home))
		}
	}
}

func serverState(home string) *ServerState {
	raw, err := os.ReadFile(serverFile(home))
	if err != nil {
		return &ServerState{}
	}
	var info serverInfo
	if json.Unmarshal(raw, &info) != nil || info.PID == 0 {
		return &ServerState{}
	}
	alive := syscall.Kill(info.PID, 0) == nil
	if alive {
		c := http.Client{Timeout: time.Second}
		resp, err := c.Get("http://" + info.Addr + "/api/status")
		alive = err == nil && resp.StatusCode == 200
		if resp != nil {
			resp.Body.Close()
		}
	}
	if !alive {
		return &ServerState{}
	}
	return &ServerState{Running: true, PID: info.PID, Addr: info.Addr, Started: info.Started, Build: info.Build,
		Outdated: info.Build != build()}
}

func (s *ServerState) String() string {
	if !s.Running {
		return "not running · start it: nohup crew serve >> ~/.local/share/crew/serve.log 2>&1 < /dev/null & disown"
	}
	since := s.Started
	if t, err := time.Parse(time.RFC3339, s.Started); err == nil {
		since = t.Local().Format("Jan 2 15:04")
	}
	line := fmt.Sprintf("running on http://%s (pid %d, since %s, build %s)", s.Addr, s.PID, since, s.Build)
	if s.Outdated {
		line += fmt.Sprintf(" · older than this crew (build %s): restart it", build())
	}
	return line
}

// colorOn: color only for a terminal, and never with NO_COLOR set (https://no-color.org)
func colorOn(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// colored is String with the state in color: green running, yellow outdated, red not running
func (s *ServerState) colored(on bool) string {
	text := s.String()
	if !on {
		return text
	}
	const green, yellow, red, reset = "\033[32m", "\033[33m", "\033[31m", "\033[0m"
	switch {
	case !s.Running:
		return red + text + reset
	case s.Outdated:
		head, tail, _ := strings.Cut(text, " · ")
		return green + head + reset + " · " + yellow + tail + reset
	default:
		return green + text + reset
	}
}
