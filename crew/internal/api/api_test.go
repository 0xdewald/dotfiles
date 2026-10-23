package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xdewald/dotfiles/crew/internal/board"
)

func setup(t *testing.T) (*board.Board, *board.Agent, *httptest.Server) {
	t.Helper()
	t.Setenv("CREW_NO_NOTIFY", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	root := t.TempDir()
	fake := filepath.Join(root, "claude")
	os.WriteFile(fake, []byte("#!/bin/sh\nif [ \"$1\" = agents ]; then echo '[{\"sessionId\":\"abc12345-0000-4000-8000-000000000000\",\"kind\":\"background\",\"status\":\"idle\"}]'; exit 0; fi\necho \"backgrounded · abc12345 · agent\"\n"), 0o755)
	t.Setenv("CREW_CLAUDE", fake)
	slot := filepath.Join(root, "slot")
	if out, err := exec.Command("git", "init", "-b", "main", slot).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	b, err := board.Open(filepath.Join(root, "home"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	b.AddSlot("s1", slot, 0)
	it, _ := b.NewItem("", "x", "core", "agent/VD-9")
	b.MoveItem(it.ID, "queued", false)
	a, err := b.StartAgent(board.StartOpts{ItemID: it.ID})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(b))
	t.Cleanup(srv.Close)
	return b, a, srv
}

func TestByBranch(t *testing.T) {
	_, a, srv := setup(t)
	resp, err := http.Get(srv.URL + "/api/agents/by-branch?branch=agent/VD-9&repo=core")
	if err != nil {
		t.Fatal(err)
	}
	var got board.Agent
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if resp.StatusCode != 200 || got.ID != a.ID || got.SessionID != a.SessionID || got.Branch != "agent/VD-9" || got.BgID != a.SessionID[:8] {
		t.Fatalf("%d %+v", resp.StatusCode, got)
	}
	resp, _ = http.Get(srv.URL + "/api/agents/by-branch?branch=nope")
	if resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
	resp, _ = http.Get(srv.URL + "/api/agents/by-branch")
	if resp.StatusCode != 400 {
		t.Fatal(resp.StatusCode)
	}
}

func TestPostsAndEvents(t *testing.T) {
	b, a, srv := setup(t)
	req, _ := http.NewRequest("GET", srv.URL+"/api/events?since=0", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sawStart := make(chan bool, 1)
	go func() {
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "event: agent.start") {
				sawStart <- true
				return
			}
		}
	}()
	select {
	case <-sawStart:
	case <-time.After(3 * time.Second):
		t.Fatal("no agent.start event on the stream")
	}

	post := func(path, body string) int {
		r, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if c := post("/api/items/1/move", `{"stage":"merged"}`); c != 409 {
		t.Fatal("bad move accepted:", c)
	}
	if c := post("/api/items/1/move", `{"stage":"pushed"}`); c != 200 {
		t.Fatal(c)
	}
	if c := post("/api/comments", `[{"branch":"agent/VD-9","file":"a.go","start_line":2,"excerpt":["x"],"text":"hm"}]`); c != 200 {
		t.Fatal(c)
	}
	if c := post("/api/agents/"+a.Name+"/attach", `{"on":true}`); c != 200 {
		t.Fatal(c)
	}
	if c := post("/api/messages", `{"to":"`+a.Name+`","text":"hi"}`); c != 200 {
		t.Fatal(c)
	}
	cs, _ := b.Comments(board.CommentFilter{ItemID: 1})
	ms, _ := b.Messages(a.Name, "pending")
	if len(cs) != 1 || len(ms) != 1 {
		t.Fatal(len(cs), len(ms))
	}
}

func TestDecisions(t *testing.T) {
	b, a, srv := setup(t)
	d1, _ := b.AskDecision(a.ID, "Keep it?", []string{"yes", "no"}, "yes", "safer")
	d2, _ := b.AskDecision(a.ID, "Name?", nil, "", "")
	resp, err := http.Get(srv.URL + "/api/decisions?agent=" + fmt.Sprint(a.ID))
	if err != nil {
		t.Fatal(err)
	}
	var ds []board.Decision
	json.NewDecoder(resp.Body).Decode(&ds)
	resp.Body.Close()
	if len(ds) != 2 || ds[0].Question != "Keep it?" || ds[0].Options[1] != "no" {
		t.Fatalf("%+v", ds)
	}
	answer := func(id int64, body string) (int, map[string]json.RawMessage) {
		r, err := http.Post(fmt.Sprintf("%s/api/decisions/%d/answer", srv.URL, id), "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	if c, out := answer(d1.ID, `{"accept":true}`); c != 200 || string(out["message"]) != "null" {
		t.Fatal(c, out)
	}
	if c, _ := answer(d1.ID, `{"answer":"again"}`); c != 409 {
		t.Fatal("answered twice:", c)
	}
	if c, _ := answer(d2.ID, `{"answer":""}`); c != 409 {
		t.Fatal("empty answer accepted:", c)
	}
	c, out := answer(d2.ID, `{"answer":"crew"}`)
	var m board.Message
	json.Unmarshal(out["message"], &m)
	if c != 200 || !strings.Contains(m.Text, "1. Keep it?\n   → yes\n2. Name?\n   → crew") {
		t.Fatal(c, string(out["message"]))
	}
}

// The dashboard page, the event log it starts from, and the unnamed stream it follows
func TestDashboard(t *testing.T) {
	b, _, srv := setup(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || !strings.Contains(string(page), "/api/log") {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp, _ := http.Get(srv.URL + "/nope"); resp.StatusCode != 404 {
		t.Fatal("unknown paths should 404, not show the dashboard:", resp.StatusCode)
	}

	for i := 0; i < 5; i++ {
		b.Event("test.ping", nil, nil, map[string]any{"n": i})
	}
	resp, _ = http.Get(srv.URL + "/api/log?limit=3")
	var evs []board.Event
	json.NewDecoder(resp.Body).Decode(&evs)
	resp.Body.Close()
	last, _ := b.LastEventID()
	if len(evs) != 3 || evs[2].ID != last || evs[0].ID != last-2 {
		t.Fatalf("log: %+v", evs)
	}

	resp, err = http.Get(srv.URL + fmt.Sprintf("/api/events?unnamed=1&since=%d", last-1))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	var lines []string
	for sc.Scan() && len(lines) < 3 {
		if l := sc.Text(); l != "" && !strings.HasPrefix(l, ":") {
			lines = append(lines, l)
		}
		if len(lines) == 2 {
			break
		}
	}
	if len(lines) != 2 || lines[0] != fmt.Sprintf("id: %d", last) || !strings.HasPrefix(lines[1], "data: ") {
		t.Fatalf("unnamed stream: %q", lines)
	}
}

// crew's docs are built in and served for the dashboard's Guide tab
func TestGuide(t *testing.T) {
	_, _, srv := setup(t)
	resp, _ := http.Get(srv.URL + "/api/guide")
	var list []struct{ Name, Title string }
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 3 || list[0].Name != "TRAINING.md" {
		t.Fatalf("%+v", list)
	}
	resp, _ = http.Get(srv.URL + "/api/guide/TRAINING.md")
	var g struct{ Text string }
	json.NewDecoder(resp.Body).Decode(&g)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(g.Text, "#") {
		t.Fatalf("%d %.80q", resp.StatusCode, g.Text)
	}
	for _, bad := range []string{"go.mod", "..%2Fgo.mod", "nope.md"} {
		if resp, _ := http.Get(srv.URL + "/api/guide/" + bad); resp.StatusCode != 404 {
			t.Fatalf("%s: %d", bad, resp.StatusCode)
		}
	}
}
