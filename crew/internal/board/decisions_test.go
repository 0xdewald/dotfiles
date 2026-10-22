package board

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// An agent's questions are answered on the board one by one; it hears back once, with all of them
func TestDecisionsAnsweredTogether(t *testing.T) {
	b, r, f, _, _, it := startSetup(t)
	a, err := b.StartAgent(StartOpts{ItemID: it.ID})
	if err != nil {
		t.Fatal(err)
	}
	hook(t, b, "UserPromptSubmit", a.SessionID, "")
	d1, err := b.AskDecision(a.ID, "Keep the v1 endpoint?", []string{"keep it", "remove it"}, "keep it", "clients still call it")
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := b.AskDecision(a.ID, "Which flag name?", []string{"--fast", "--quick"}, "--fast", "matches the other tools")
	if d1.Status != "open" || d1.Agent != a.Name || d1.ItemID == nil || *d1.ItemID != it.ID || !slices.Equal(d1.Options, []string{"keep it", "remove it"}) {
		t.Fatalf("%+v", d1)
	}
	if a, _ = b.AgentByID(a.ID); a.Status != StatusWorking || a.Decisions != 2 {
		t.Fatalf("asking mid-turn changed the agent: %+v", a)
	}
	// it ends its turn: waiting on a human, one notification about the decisions
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "Two decisions on the board."))
	if a, _ = b.AgentByID(a.ID); a.Status != StatusAwaitingDecision {
		t.Fatal(a.Status)
	}
	if len(r.notified) != 1 || !strings.HasPrefix(r.notified[0], "crew: "+a.Name+" has 2 decisions: Keep the v1 endpoint?") {
		t.Fatalf("notified %q", r.notified)
	}
	open, _ := b.Decisions(DecisionFilter{Agent: a.Name})
	if len(open) != 2 {
		t.Fatalf("open = %d", len(open))
	}

	calls := len(f.Calls(t))
	d1, m, err := b.AnswerDecision(d1.ID, "keep it, but log a deprecation warning", "you")
	if err != nil || m != nil || d1.Status != "answered" || d1.AnsweredBy != "you" || d1.MessageID != nil {
		t.Fatalf("first answer: %v %+v %+v", err, d1, m)
	}
	if len(f.Calls(t)) != calls {
		t.Fatal("agent resumed before all its decisions were answered")
	}
	if a, _ = b.AgentByID(a.ID); a.Decisions != 1 || a.Status != StatusAwaitingDecision {
		t.Fatalf("%+v", a)
	}
	if _, _, err := b.AnswerDecision(d1.ID, "again", "you"); err == nil {
		t.Fatal("answered an answered decision")
	}

	d2, m, err = b.AcceptDecision(d2.ID, "you")
	if err != nil || m == nil || d2.Answer != "--fast" || d2.MessageID == nil || *d2.MessageID != m.ID {
		t.Fatalf("accept: %v %+v %+v", err, d2, m)
	}
	want := "Answers to your decisions:\n1. Keep the v1 endpoint?\n   → keep it, but log a deprecation warning\n2. Which flag name?\n   → --fast"
	if m.Text != want || m.Status != "delivered" || m.From != "you" {
		t.Fatalf("message %+v", m)
	}
	cs := f.Calls(t)
	if len(cs) != calls+2 || cs[calls].Args[0] != "stop" {
		t.Fatalf("calls %d", len(cs))
	}
	if last := cs[len(cs)-1].Args; last[1] != "--resume" || last[len(last)-1] != want {
		t.Fatalf("resume %q", last)
	}
	if ms, _ := b.Messages(a.Name, ""); len(ms) != 1 {
		t.Fatalf("messages = %d, want one combined", len(ms))
	}
	if a, _ = b.AgentByID(a.ID); a.Status != StatusWorking || a.Decisions != 0 {
		t.Fatalf("%+v", a)
	}

	// a later batch only carries its own answers; a cancelled question isn't in it
	d3, _ := b.AskDecision(a.ID, "Squash commits?", nil, "", "")
	d4, _ := b.AskDecision(a.ID, "Rename the table?", nil, "", "")
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "More questions."))
	if _, _, err := b.AcceptDecision(d3.ID, "you"); err == nil {
		t.Fatal("accepted a decision without a recommendation")
	}
	if _, _, err := b.RejectDecision(d3.ID, "", "you"); err == nil {
		t.Fatal("rejected a decision without a recommendation")
	}
	b.AnswerDecision(d3.ID, "yes", "director")
	_, m, err = b.CancelDecision(d4.ID)
	if err != nil || m == nil || m.Text != "Answers to your decisions:\n1. Squash commits?\n   → yes" || m.From != "director" {
		t.Fatalf("cancel completing a batch: %v %+v", err, m)
	}
	all, _ := b.Decisions(DecisionFilter{Agent: a.Name, Status: "all"})
	if len(all) != 4 || all[3].Status != "cancelled" {
		t.Fatalf("%+v", all)
	}
}

// Withdrawing every question leaves the agent idle, with nothing sent
func TestCancelOnlyDecision(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "done"))
	d, _ := b.AskDecision(a.ID, "Ship it?", []string{"yes", "no"}, "yes", "tests pass")
	if a, _ = b.AgentByID(a.ID); a.Status != StatusAwaitingDecision {
		t.Fatalf("asked between turns: %s", a.Status)
	}
	n := len(f.Calls(t))
	if _, m, err := b.CancelDecision(d.ID); err != nil || m != nil {
		t.Fatal(err, m)
	}
	if a, _ = b.AgentByID(a.ID); a.Status != StatusIdle || len(f.Calls(t)) != n {
		t.Fatalf("%+v", a)
	}
}

// Agents waiting on a human's decision never go stale
func TestSweepSparesAwaitingDecision(t *testing.T) {
	b, _, _, _, _, it := startSetup(t)
	it2, _ := b.NewItem("", "other", "", "agent/other")
	b.MoveItem(it2.ID, "queued", false)
	waiting, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	idle, err := b.StartAgent(StartOpts{ItemID: it2.ID})
	if err != nil {
		t.Fatal(err)
	}
	b.AskDecision(waiting.ID, "Which approach?", []string{"a", "b"}, "a", "simpler")
	hook(t, b, "Stop", waiting.SessionID, writeTranscript(t, "asked"))
	hook(t, b, "Stop", idle.SessionID, writeTranscript(t, "done"))
	t.Setenv("CREW_STALE_AFTER", "1ns")
	time.Sleep(5 * time.Millisecond)
	stale, _ := b.Sweep()
	if len(stale) != 1 || stale[0].ID != idle.ID {
		t.Fatalf("stale = %+v", stale)
	}
	if w, _ := b.AgentByID(waiting.ID); w.Status != StatusAwaitingDecision {
		t.Fatalf("waiting agent: %s", w.Status)
	}
}

// Workers get crew's MCP server and their instructions on start and on every resume
func TestWorkerGetsCrewTools(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, err := b.StartAgent(StartOpts{ItemID: it.ID})
	if err != nil {
		t.Fatal(err)
	}
	b.PostMessage(PostOpts{To: a.Name, Text: "go"}) // starting: waits
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "ready"))
	cs := f.Calls(t)
	start, resume := cs[0].Args, cs[len(cs)-1].Args
	file := filepath.Join(b.Home, "agents", fmt.Sprint(a.ID), "mcp.json")
	for _, args := range [][]string{start, resume} {
		i := slices.Index(args, "--mcp-config")
		j := slices.Index(args, "--append-system-prompt")
		if i < 0 || args[i+1] != file || j != i+2 || slices.Index(args, "--permission-mode") != j+2 {
			t.Fatalf("args %q", args)
		}
		p := args[j+1]
		if !strings.Contains(p, "crew agent "+a.Name) || !strings.Contains(p, "branch agent/VD-1") ||
			!strings.Contains(p, "`ask_decision`") || !strings.Contains(p, "`report_done`") {
			t.Fatalf("instructions:\n%s", p)
		}
	}
	if resume[1] != "--resume" || resume[len(resume)-1] != "go" {
		t.Fatalf("resume %q", resume)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	c := cfg.MCPServers["crew"]
	exe, _ := os.Executable()
	if !filepath.IsAbs(c.Command) || c.Command != exe || !slices.Equal(c.Args, []string{"mcp"}) ||
		c.Env["CREW_AGENT_ID"] != fmt.Sprint(a.ID) || c.Env["CREW_AGENT_NAME"] != a.Name || c.Env["CREW_HOME"] != b.Home {
		t.Fatalf("mcp.json:\n%s", raw)
	}
	// roles without instructions still get the tools
	if p := rolePrompt(&Agent{Role: "reviewer"}); p != "" {
		t.Fatalf("reviewer prompt %q", p)
	}
}

func TestRejectDecision(t *testing.T) {
	b, _, _, _, _, it := startSetup(t)
	a, err := b.StartAgent(StartOpts{ItemID: it.ID})
	if err != nil {
		t.Fatal(err)
	}
	d1, _ := b.AskDecision(a.ID, "Which flag?", []string{"--fast", "--safe"}, "--fast", "quicker")
	d2, _ := b.AskDecision(a.ID, "Squash?", []string{"yes", "no"}, "yes", "tidier")
	b.RejectDecision(d1.ID, "", "you")
	_, m, err := b.RejectDecision(d2.ID, "  keep the history  ", "you")
	want := "Answers to your decisions:\n1. Which flag?\n   → Rejected: --fast.\n2. Squash?\n   → Rejected: yes. keep the history"
	if err != nil || m == nil || m.Text != want {
		t.Fatalf("%v %+v", err, m)
	}
}
