package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xdewald/dotfiles/crew/internal/board"
)

func env(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CREW_HOME", filepath.Join(root, "home"))
	t.Setenv("CREW_CONFIG", filepath.Join(root, "none"))
	t.Setenv("CREW_NO_NOTIFY", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	for _, k := range []string{"JIRA_BASE_URL", "JIRA_EMAIL", "JIRA_API_TOKEN", "CREW_AGENT_ID"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	fake := filepath.Join(root, "claude")
	os.WriteFile(fake, []byte("#!/bin/sh\nif [ \"$1\" = agents ]; then echo '[{\"sessionId\":\"abc12345-0000-4000-8000-000000000000\",\"kind\":\"background\",\"status\":\"idle\"}]'; exit 0; fi\necho \"$@\" >> \""+root+"/calls\"\np=\"$CLAUDE_CONFIG_DIR/projects/$(pwd -P | sed 's/[^a-zA-Z0-9]/-/g')\"; mkdir -p \"$p\"; touch \"$p/abc12345-0000-4000-8000-000000000000.jsonl\"\necho \"backgrounded · abc12345 · agent\"\n"), 0o755)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude-home"))
	t.Setenv("CREW_CLAUDE", fake)
	return root
}

func crew(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return out.String(), errb.String(), code
}

func mustJSON(t *testing.T, v any, stdin string, args ...string) {
	t.Helper()
	out, errs, code := crew(t, stdin, append(args, "--json")...)
	if code != 0 {
		t.Fatalf("crew %v: %d %s", args, code, errs)
	}
	if v != nil {
		if err := json.Unmarshal([]byte(out), v); err != nil {
			t.Fatalf("crew %v: %v\n%s", args, err, out)
		}
	}
}

func TestHookUnknownSessionSilent(t *testing.T) {
	env(t)
	mustJSON(t, nil, "", "init")
	out, errs, code := crew(t, `{"hook_event_name":"Stop","session_id":"nope","transcript_path":"/x","cwd":"/"}`, "event", "--hook")
	if out != "" || errs != "" || code != 0 {
		t.Fatalf("out=%q err=%q code=%d", out, errs, code)
	}
	// garbage and no board are silent too
	if out, _, code := crew(t, "not json", "event", "--hook"); out != "" || code != 0 {
		t.Fatal(out, code)
	}
}

func TestTicketFromFreeText(t *testing.T) {
	env(t)
	var tk struct {
		ID      int64  `json:"id"`
		JiraKey string `json:"jira_key"`
		Text    string `json:"text"`
	}
	_, errs, code := crew(t, "", "ticket", "add", "read", "jira", "ticket", "VD-21582,", "focus", "on", "X")
	if code != 0 || !strings.Contains(errs, "JIRA_") {
		t.Fatal(code, errs)
	}
	mustJSON(t, &tk, "", "ticket", "show", "VD-21582")
	if tk.JiraKey != "VD-21582" || !strings.Contains(tk.Text, "focus on X") {
		t.Fatalf("%+v", tk)
	}
	_, errs, code = crew(t, "", "item", "move", "99", "coding")
	if code == 0 || errs == "" {
		t.Fatal("expected error for unknown item")
	}
}

func TestAgentFlowCLI(t *testing.T) {
	root := env(t)
	slot := filepath.Join(root, "slot")
	if out, err := exec.Command("git", "init", "-b", "main", slot).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	mustJSON(t, nil, "", "slot", "add", "s1", slot)
	mustJSON(t, nil, "", "ticket", "add", "--jira", "VD-1", "--text", "do X")
	var it struct {
		ID     int64  `json:"id"`
		Branch string `json:"branch"`
	}
	mustJSON(t, &it, "", "item", "new", "--ticket", "VD-1", "--repo", "core")
	mustJSON(t, nil, "", "item", "move", "1", "queued")
	if _, errs, code := crew(t, "", "item", "move", "1", "merged"); code != 1 || !strings.Contains(errs, "--force") {
		t.Fatal(code, errs)
	}
	var a struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		SessionID string `json:"session_id"`
		BgID      string `json:"bg_id"`
		Branch    string `json:"branch"`
		Status    string `json:"status"`
	}
	mustJSON(t, &a, "", "agent", "start", "1")
	if a.BgID != a.SessionID[:8] || a.Branch != "agent/VD-1" || a.Status != "starting" {
		t.Fatalf("%+v", a)
	}
	var byBranch struct {
		ID int64 `json:"id"`
	}
	mustJSON(t, &byBranch, "", "agent", "show", "--branch", "agent/VD-1", "--repo", "core")
	if byBranch.ID != a.ID {
		t.Fatal(byBranch)
	}
	if _, _, code := crew(t, "", "agent", "show", "--branch", "agent/none"); code != 1 {
		t.Fatal("expected not found")
	}

	// Stop hook → idle; then comments sent as a review resume the agent
	tr := filepath.Join(root, "t.jsonl")
	os.WriteFile(tr, []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Ready."}]}}`+"\n"), 0o644)
	payload, _ := json.Marshal(map[string]string{"hook_event_name": "Stop", "session_id": a.SessionID, "transcript_path": tr})
	if out, _, code := crew(t, string(payload), "event", "--hook"); out != "" || code != 0 {
		t.Fatal(out, code)
	}
	var res struct {
		Comments []struct {
			ID      int64    `json:"id"`
			Excerpt []string `json:"excerpt"`
			Status  string   `json:"status"`
			ItemID  *int64   `json:"item_id"`
		} `json:"comments"`
		Message struct {
			Status string `json:"status"`
		} `json:"message"`
	}
	mustJSON(t, &res, `[{"repo":"core","branch":"agent/VD-1","commit":"abc","file":"a.go","start_line":3,"end_line":4,"excerpt":["a","b"],"text":"why","author":"you"}]`,
		"comment", "add", "--stdin-json", "--send")
	if len(res.Comments) != 1 || res.Comments[0].Status != "sent" || res.Message.Status != "delivered" || len(res.Comments[0].Excerpt) != 2 || *res.Comments[0].ItemID != it.ID {
		t.Fatalf("%+v", res)
	}
	var cs []struct {
		Excerpt []string `json:"excerpt"`
	}
	mustJSON(t, &cs, "", "comment", "ls", "--branch", "agent/VD-1")
	if len(cs) != 1 || cs[0].Excerpt[1] != "b" {
		t.Fatalf("%+v", cs)
	}
	calls, _ := os.ReadFile(filepath.Join(root, "calls"))
	if !strings.Contains(string(calls), "stop "+a.SessionID[:8]) || !strings.Contains(string(calls), "--bg --resume "+a.SessionID+" --mcp-config ") ||
		!strings.Contains(string(calls), " --permission-mode auto I reviewed your changes") {
		t.Fatalf("calls:\n%s", calls)
	}
}

func TestDecisionCLI(t *testing.T) {
	root := env(t)
	slot := filepath.Join(root, "slot")
	if out, err := exec.Command("git", "init", "-b", "main", slot).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	mustJSON(t, nil, "", "slot", "add", "s1", slot)
	mustJSON(t, nil, "", "item", "new", "--title", "x", "--branch", "agent/x")
	var ag struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		SessionID string `json:"session_id"`
	}
	mustJSON(t, &ag, "", "agent", "start", "1")
	// the agent asks (through crew mcp's ask_decision), then ends its turn
	b, err := board.Open(os.Getenv("CREW_HOME"))
	if err != nil {
		t.Fatal(err)
	}
	b.AskDecision(ag.ID, "Keep v1?", []string{"keep", "drop"}, "keep", "clients use it")
	b.AskDecision(ag.ID, "Flag name?", []string{"--fast", "--quick"}, "--fast", "")
	b.Close()
	payload, _ := json.Marshal(map[string]string{"hook_event_name": "Stop", "session_id": ag.SessionID, "transcript_path": "/none"})
	crew(t, string(payload), "event", "--hook")

	var as []struct {
		Status    string `json:"status"`
		Decisions int    `json:"decisions"`
	}
	mustJSON(t, &as, "", "agent", "ls")
	if as[0].Status != "awaiting-decision" || as[0].Decisions != 2 {
		t.Fatalf("%+v", as)
	}
	if out, _, _ := crew(t, "", "agent", "ls"); !strings.Contains(out, "DECISIONS") {
		t.Fatal(out)
	}
	var ds []struct {
		ID       int64    `json:"id"`
		Agent    string   `json:"agent"`
		Options  []string `json:"options"`
		Question string   `json:"question"`
	}
	mustJSON(t, &ds, "", "decision", "ls", "--agent", ag.Name)
	if len(ds) != 2 || ds[0].Agent != ag.Name || ds[1].Options[1] != "--quick" {
		t.Fatalf("%+v", ds)
	}
	if out, _, code := crew(t, "", "decision", "show", "1"); code != 0 || !strings.Contains(out, "keep  (recommended)") || !strings.Contains(out, "clients use it") {
		t.Fatal(out)
	}
	var r struct {
		Decision struct {
			Status string `json:"status"`
			Answer string `json:"answer"`
		} `json:"decision"`
		Message *struct {
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"message"`
	}
	mustJSON(t, &r, "", "decision", "answer", "1", "drop", "it")
	if r.Decision.Answer != "drop it" || r.Message != nil {
		t.Fatalf("%+v", r)
	}
	if _, errs, code := crew(t, "", "decision", "answer", "1", "again"); code != 1 || !strings.Contains(errs, "already answered") {
		t.Fatal(code, errs)
	}
	mustJSON(t, &r, "", "decision", "answer", "2", "--accept")
	want := "Answers to your decisions:\n1. Keep v1?\n   → drop it\n2. Flag name?\n   → --fast"
	if r.Decision.Answer != "--fast" || r.Message == nil || r.Message.Text != want || r.Message.Status != "delivered" {
		t.Fatalf("%+v %+v", r, r.Message)
	}
	calls, _ := os.ReadFile(filepath.Join(root, "calls"))
	if n := strings.Count(string(calls), "Answers to your decisions:"); n != 1 {
		t.Fatalf("answers sent %d times:\n%s", n, calls)
	}
	var none []any
	mustJSON(t, &none, "", "decision", "ls")
	if len(none) != 0 {
		t.Fatal(none)
	}
	mustJSON(t, &none, "", "decision", "ls", "--status", "all")
	if len(none) != 2 {
		t.Fatal(none)
	}
}
