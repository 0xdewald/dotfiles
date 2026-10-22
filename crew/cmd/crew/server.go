package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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
		return "not running · start it: crew serve start"
	}
	since := s.Started
	if t, err := time.Parse(time.RFC3339, s.Started); err == nil {
		since = t.Local().Format("Jan 2 15:04")
	}
	line := fmt.Sprintf("running on http://%s (pid %d, since %s, build %s)", s.Addr, s.PID, since, s.Build)
	if s.Outdated {
		line += fmt.Sprintf(" · older than this crew (build %s): crew serve restart", build())
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

func serverLog(home string) string { return filepath.Join(home, "serve.log") }

// serveStart runs `crew serve` detached (its own session, so closing the terminal doesn't stop
// it), logging to <home>/serve.log, and waits until it answers.
func (a *app) serveStart(args []string) error {
	home := a.b.Home
	if s := serverState(home); s.Running {
		fmt.Fprintf(a.stdout, "already %s\n", s.colored(colorOn(a.stdout)))
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(serverLog(home), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(self, append([]string{"serve"}, args...)...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	cmd.Process.Release()
	for i := 0; i < 50; i++ { // up to 5s
		if s := serverState(home); s.Running {
			fmt.Fprintf(a.stdout, "server %s\nlogs: %s (crew serve logs)\n", s.colored(colorOn(a.stdout)), serverLog(home))
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("crew serve didn't come up; see %s", serverLog(home))
}

// serveStop stops the server crew status reports (SIGTERM, then SIGKILL after 5s).
func (a *app) serveStop() error {
	home := a.b.Home
	s := serverState(home)
	if !s.Running {
		os.Remove(serverFile(home)) // a leftover record from a crash
		fmt.Fprintln(a.stdout, "server not running")
		return nil
	}
	syscall.Kill(s.PID, syscall.SIGTERM)
	for i := 0; i < 50; i++ {
		if syscall.Kill(s.PID, 0) != nil {
			fmt.Fprintf(a.stdout, "stopped server (pid %d)\n", s.PID)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	syscall.Kill(s.PID, syscall.SIGKILL)
	os.Remove(serverFile(home))
	fmt.Fprintf(a.stdout, "killed server (pid %d): it didn't stop within 5s\n", s.PID)
	return nil
}

// serveLogs prints the last 40 lines of the server log.
func (a *app) serveLogs() error {
	f, err := os.Open(serverLog(a.b.Home))
	if err != nil {
		return fmt.Errorf("no server log yet (%s)", serverLog(a.b.Home))
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > 40 {
			lines = lines[1:]
		}
	}
	for _, l := range lines {
		fmt.Fprintln(a.stdout, l)
	}
	return nil
}
