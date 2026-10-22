package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerState(t *testing.T) {
	home := t.TempDir()
	if s := serverState(home); s.Running || !strings.Contains(s.String(), "not running") {
		t.Fatalf("no record: %+v", s)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	write := func(pid int, b string) {
		raw, _ := json.Marshal(serverInfo{PID: pid, Addr: addr, Started: "2026-10-22T15:00:00Z", Build: b})
		os.WriteFile(filepath.Join(home, "serve.json"), raw, 0o644)
	}
	write(os.Getpid(), build())
	if s := serverState(home); !s.Running || s.Outdated || !strings.Contains(s.String(), "running on http://"+addr) {
		t.Fatalf("running: %+v %s", s, s)
	}
	write(os.Getpid(), "0000000")
	if s := serverState(home); !s.Outdated || !strings.Contains(s.String(), "crew serve restart") {
		t.Fatalf("older build not flagged: %s", s)
	}
	write(999999, build()) // no such process: a leftover record
	if s := serverState(home); s.Running {
		t.Fatal("leftover record shown as running")
	}
}

func TestServerStateColor(t *testing.T) {
	off := &ServerState{}
	if c := off.colored(true); !strings.HasPrefix(c, "\033[31m") {
		t.Fatalf("not running should be red: %q", c)
	}
	if c := off.colored(false); strings.Contains(c, "\033[") {
		t.Fatalf("no color when off: %q", c)
	}
	old := &ServerState{Running: true, Addr: "x", Build: "0000000", Outdated: true}
	if c := old.colored(true); !strings.Contains(c, "\033[32m") || !strings.Contains(c, "\033[33m") {
		t.Fatalf("outdated should be green + yellow: %q", c)
	}
}
