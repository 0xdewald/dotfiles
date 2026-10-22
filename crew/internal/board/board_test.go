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

func TestTransitions(t *testing.T) {
	b, _ := newBoard(t)
	it, err := b.NewItem("", "x", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if it.Stage != "new" || it.Branch != fmt.Sprintf("agent/%d", it.ID) {
		t.Fatalf("%+v", it)
	}
	if _, err := b.MoveItem(it.ID, "coding", false); err == nil {
		t.Fatal("new → coding should be refused")
	}
	for _, s := range []string{"queued", "coding", "pushed", "reviewing", "coding"} {
		if _, err := b.MoveItem(it.ID, s, false); err != nil {
			t.Fatal(err)
		}
	}
	it, _ = b.Item(it.ID)
	if it.Rounds != 1 {
		t.Fatalf("rounds = %d", it.Rounds)
	}
	if _, err := b.MoveItem(it.ID, "merged", false); err == nil {
		t.Fatal("coding → merged should be refused")
	}
	if it, err = b.MoveItem(it.ID, "merged", true); err != nil || it.Stage != "merged" {
		t.Fatal(err)
	}
	if _, err := b.MoveItem(it.ID, "closed", false); err != nil {
		t.Fatal("any → closed must be allowed:", err)
	}
	if _, err := b.MoveItem(it.ID, "bogus", true); err == nil {
		t.Fatal("unknown stage accepted")
	}
	ev, _ := b.Events(0, 0)
	moves := 0
	for _, e := range ev {
		if e.Type == "item.move" {
			moves++
		}
	}
	if moves != 7 {
		t.Fatalf("move events = %d", moves)
	}
}

func TestItemDefaultsFromTicket(t *testing.T) {
	b, _ := newBoard(t)
	tk, err := b.AddTicket("VD-7", "", "do x", nil)
	if err != nil {
		t.Fatal(err)
	}
	it, err := b.NewItem("VD-7", "", "core", "")
	if err != nil {
		t.Fatal(err)
	}
	if it.Branch != "agent/VD-7" || *it.TicketID != tk.ID || it.JiraKey != "VD-7" || it.Title != "VD-7" {
		t.Fatalf("%+v", it)
	}
}

func TestSlotDirChecks(t *testing.T) {
	b, _ := newBoard(t)
	dir := t.TempDir()
	if _, err := b.AddSlot("missing", dir+"/nope", -1); err == nil || !strings.Contains(err.Error(), "git clone") {
		t.Fatalf("missing folder: %v", err)
	}
	os.Mkdir(dir+"/empty", 0o755)
	if _, err := b.AddSlot("empty", dir+"/empty", -1); err == nil {
		t.Fatal("folder without a checkout accepted")
	}
	git(t, dir, "init", "-q", "multi/core")
	if _, err := b.AddSlot("multi", dir+"/multi", -1); err != nil {
		t.Fatalf("folder holding a checkout: %v", err)
	}
	git(t, dir, "init", "-q", "--bare", "origin.git")
	s, err := b.AddSlotClone("cloned", dir+"/slots/one/core", dir+"/origin.git", -1)
	if err != nil || s.Path != dir+"/slots/one/core" {
		t.Fatalf("clone: %v %+v", err, s)
	}
	if _, err := b.AddSlotClone("again", dir+"/slots/one/core", dir+"/origin.git", -1); err == nil {
		t.Fatal("clone into an existing folder accepted")
	}
}

func TestSlotLeasing(t *testing.T) {
	b, _ := newBoard(t)
	dir := t.TempDir()
	for _, d := range []string{"a", "b", "c"} {
		git(t, dir, "init", "-q", d)
	}
	s1, err := b.AddSlot("a", dir+"/a", -1)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := b.AddSlot("b", dir+"/b", -1)
	if s1.PortOffset != 0 || s2.PortOffset != 100 {
		t.Fatal(s1.PortOffset, s2.PortOffset)
	}
	if _, err := b.AddSlot("a", dir+"/c", -1); err == nil {
		t.Fatal("duplicate slot name accepted")
	}
	if _, err := b.LeaseSlot("a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.LeaseSlot("a", 2); err == nil {
		t.Fatal("double lease accepted")
	}
	if _, err := b.LeaseSlot("a", 1); err != nil {
		t.Fatal("re-lease by holder refused:", err)
	}
	s, _ := b.ReleaseSlot("a")
	if s.LeasedBy != nil {
		t.Fatal("not released")
	}
	if _, err := b.LeaseSlot("a", 2); err != nil {
		t.Fatal(err)
	}
}

func TestBranchLock(t *testing.T) {
	b, _ := newBoard(t)
	if err := b.LockBranch("core", "agent/VD-1", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.LockBranch("core", "agent/VD-1", 2, 2); err == nil {
		t.Fatal("second slot got the same repo+branch")
	}
	if err := b.LockBranch("other", "agent/VD-1", 2, 2); err != nil {
		t.Fatal("different repo should be fine:", err)
	}
}

// setup: two slots cloned from one origin, a queued item.
func startSetup(t *testing.T) (*Board, *recExec, *Fake, string, []string, *Item) {
	b, r := newBoard(t)
	f := newFake(t)
	origin, clones := repoSetup(t, 2)
	for i, c := range clones {
		if _, err := b.AddSlot(fmt.Sprint("s", i+1), c, -1); err != nil {
			t.Fatal(err)
		}
	}
	b.AddTicket("VD-1", "Do X", "", nil)
	it, err := b.NewItem("VD-1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.MoveItem(it.ID, "queued", false); err != nil {
		t.Fatal(err)
	}
	return b, r, f, origin, clones, it
}

func TestAgentStart(t *testing.T) {
	b, _, f, _, clones, it := startSetup(t)
	a, err := b.StartAgent(StartOpts{ItemID: it.ID, Slot: "s2"})
	if err != nil {
		t.Fatal(err)
	}
	calls := f.Calls(t)
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	c := calls[0]
	want := withCrew(b, a, []string{"--bg", "-n", a.Name}, "--permission-mode", "auto")
	if len(c.Args) != len(want)+1 || !slices.Equal(c.Args[:len(want)], want) || !strings.Contains(c.Args[len(want)], "Jira ticket: VD-1") {
		t.Fatalf("args = %q, want %q", c.Args, want)
	}
	if c.Cwd != clones[1] {
		t.Fatalf("cwd = %s", c.Cwd)
	}
	slug := strings.ToLower(strings.ReplaceAll(a.Name, " ", "-"))
	for k, v := range map[string]string{
		"CREW_PORT_OFFSET": "100", "CREW_PROCESS_PREFIX": slug + "-",
		"CREW_AGENT_ID": fmt.Sprint(a.ID), "CREW_HOME": b.Home,
	} {
		if c.Env[k] != v {
			t.Errorf("env %s = %q, want %q", k, c.Env[k], v)
		}
	}
	if got := git(t, clones[1], "rev-parse", "--abbrev-ref", "HEAD"); got != "agent/VD-1" {
		t.Fatalf("branch = %s", got)
	}
	if a.Status != StatusStarting || a.BgID != a.SessionID[:8] || a.Slot != "s2" || a.Branch != "agent/VD-1" || a.Stage != "coding" {
		t.Fatalf("%+v", a)
	}
	it, _ = b.Item(it.ID)
	if it.Stage != "coding" || it.AgentID == nil || *it.AgentID != a.ID {
		t.Fatalf("%+v", it)
	}
	// second item on the same branch in the other slot is refused by the lock
	it2, _ := b.NewItem("", "dup", "", "agent/VD-1")
	b.MoveItem(it2.ID, "queued", false)
	if _, err := b.StartAgent(StartOpts{ItemID: it2.ID, Slot: "s1"}); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("expected lock refusal, got %v", err)
	}
	// stop frees slot and lock
	if a, err = b.StopAgent(a.Name); err != nil {
		t.Fatal(err)
	}
	if a.Status != StatusStopped {
		t.Fatal(a.Status)
	}
	if last := f.Calls(t); !slices.Equal(last[len(last)-1].Args, []string{"stop", a.SessionID[:8]}) {
		t.Fatalf("stop args = %q", last[len(last)-1].Args)
	}
	s, _ := b.Slot("s2")
	locks, _ := b.BranchLocks()
	if s.LeasedBy != nil || len(locks) != 0 {
		t.Fatal("slot/lock not released")
	}
}

func TestAgentStartRepoSubdir(t *testing.T) {
	b, _ := newBoard(t)
	f := newFake(t)
	_, clones := repoSetup(t, 1)
	slot := filepath.Dir(clones[0])
	b.AddSlot("s", slot, 0)
	it, _ := b.NewItem("", "x", filepath.Base(clones[0]), "agent/x")
	a, err := b.StartAgent(StartOpts{ItemID: it.ID, Role: "intern"})
	if err != nil {
		t.Fatal(err)
	}
	c := f.Calls(t)[0]
	if c.Cwd != clones[0] || a.Dir != clones[0] || !slices.Contains(c.Args, "plan") {
		t.Fatalf("cwd=%s dir=%s args=%q", c.Cwd, a.Dir, c.Args)
	}
}

func writeTranscript(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.jsonl")
	lines := []any{
		map[string]any{"type": "user", "message": map[string]any{"content": "hi"}},
		map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "first"}}}},
		map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
			map[string]any{"type": "tool_use", "name": "Bash"}, map[string]any{"type": "text", "text": text}}}},
		map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use"}}}},
	}
	var sb strings.Builder
	for _, l := range lines {
		j, _ := json.Marshal(l)
		sb.Write(j)
		sb.WriteByte('\n')
	}
	os.WriteFile(p, []byte(sb.String()), 0o644)
	return p
}

func hook(t *testing.T, b *Board, event, sid, transcript string) bool {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"hook_event_name": event, "session_id": sid, "transcript_path": transcript, "cwd": "/"})
	ok, err := b.HandleHook(raw)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestHookStopDelivers(t *testing.T) {
	b, r, f, _, _, it := startSetup(t)
	a, err := b.StartAgent(StartOpts{ItemID: it.ID})
	if err != nil {
		t.Fatal(err)
	}
	hook(t, b, "UserPromptSubmit", a.SessionID, "")
	if a, _ = b.AgentByID(a.ID); a.Status != StatusWorking {
		t.Fatal(a.Status)
	}
	// busy: message stays pending
	m, err := b.PostMessage(PostOpts{To: a.Name, Text: "also fix Y"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "pending" || len(f.Calls(t)) != 1 {
		t.Fatalf("delivered while working: %+v", m)
	}
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "All done, tests pass."))
	a, _ = b.AgentByID(a.ID)
	if a.LastMessage != "All done, tests pass." {
		t.Fatalf("last = %q", a.LastMessage)
	}
	calls := f.Calls(t)
	if len(calls) != 3 {
		t.Fatalf("calls = %d", len(calls))
	}
	if !slices.Equal(calls[1].Args, []string{"stop", a.SessionID[:8]}) {
		t.Fatalf("stop first: %q", calls[1].Args)
	}
	if !slices.Equal(calls[2].Args, withCrew(b, a, []string{"--bg", "--resume", a.SessionID}, "--permission-mode", "auto", "also fix Y")) {
		t.Fatalf("resume: %q", calls[2].Args)
	}
	if calls[2].Env["CREW_AGENT_ID"] != fmt.Sprint(a.ID) {
		t.Fatal("resume env missing")
	}
	if m, _ = b.Message(m.ID); m.Status != "delivered" || m.Delivered == "" {
		t.Fatalf("%+v", m)
	}
	if a.Status != StatusWorking || a.BgID != a.SessionID[:8] {
		t.Fatalf("%+v", a)
	}
	if len(r.notified) != 0 {
		t.Fatal("notified although work was delivered")
	}
	// a plain Stop with nothing pending: idle + notification
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "Fixed Y."))
	if a, _ = b.AgentByID(a.ID); a.Status != StatusIdle || len(r.notified) != 1 || !strings.Contains(r.notified[0], a.Name) {
		t.Fatalf("%+v %q", a, r.notified)
	}
	// idle: posting delivers immediately
	b.PostMessage(PostOpts{To: a.Name, Text: "one"})
	if len(f.Calls(t)) != 5 {
		t.Fatalf("calls = %d", len(f.Calls(t)))
	}
	hook(t, b, "Notification", a.SessionID, "")
	if a, _ = b.AgentByID(a.ID); a.Status != StatusNeedsInput {
		t.Fatal(a.Status)
	}
}

func TestAttachedNotResumed(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	if _, err := b.SetAttached(a.Name, true); err != nil {
		t.Fatal(err)
	}
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "done"))
	m, _ := b.PostMessage(PostOpts{ItemID: it.ID, Text: "hello"})
	if m.Status != "pending" || len(f.Calls(t)) != 1 {
		t.Fatalf("attached agent resumed: %+v calls=%d", m, len(f.Calls(t)))
	}
	if m, _ = b.AckMessage(m.ID); m.Status != "delivered" {
		t.Fatal(m.Status)
	}
	// detaching delivers what's still pending
	b.PostMessage(PostOpts{To: a.Name, Text: "later"})
	b.SetAttached(a.Name, false)
	if c := f.Calls(t); len(c) != 3 || c[2].Args[1] != "--resume" {
		t.Fatalf("calls %d", len(c))
	}
}

func TestStoppedAgentResumes(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	b.StopAgent(a.Name)
	m, err := b.PostMessage(PostOpts{ItemID: it.ID, Text: "wake up"})
	if err != nil || m.Status != "delivered" {
		t.Fatal(err, m)
	}
	c := f.Calls(t)
	if last := c[len(c)-1].Args; last[1] != "--resume" || last[len(last)-1] != "wake up" {
		t.Fatalf("%q", last)
	}
	a, _ = b.AgentByID(a.ID)
	s, _ := b.Slot(a.Slot)
	if s.LeasedBy == nil || *s.LeasedBy != a.ID {
		t.Fatal("slot not re-leased")
	}
}

func TestHookUnknownSession(t *testing.T) {
	b, _ := newBoard(t)
	if hook(t, b, "Stop", "nope", "") {
		t.Fatal("handled unknown session")
	}
}

func TestComments(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "done"))
	cs, err := b.AddComments([]Comment{{
		Repo: "core", Branch: "agent/VD-1", Commit: "abc", File: "src/a.ts", StartLine: 3, EndLine: 4,
		Excerpt: []string{"const a = 1", "const b = 2"}, Text: "rename", Author: "you",
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := b.Comments(CommentFilter{Branch: "agent/VD-1"})
	if len(got) != 1 || !slices.Equal(got[0].Excerpt, []string{"const a = 1", "const b = 2"}) || got[0].ItemID == nil ||
		*got[0].ItemID != it.ID || got[0].Status != "open" || got[0].EndLine != 4 {
		t.Fatalf("%+v", got)
	}
	if _, err := b.SendComments(cs, ""); err != nil {
		t.Fatal(err)
	}
	c := f.Calls(t)
	args := c[len(c)-1].Args
	prompt := args[len(args)-1]
	if !strings.Contains(prompt, "`src/a.ts:3-4`") || !strings.Contains(prompt, "const b = 2") {
		t.Fatalf("prompt: %s", prompt)
	}
	if x, _ := b.Comment(cs[0].ID); x.Status != "sent" {
		t.Fatal(x.Status)
	}
	if x, _ := b.SetCommentStatus(cs[0].ID, "resolved"); x.Status != "resolved" {
		t.Fatal(x.Status)
	}
}

func TestAgentByBranch(t *testing.T) {
	b, _, _, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	got, err := b.AgentByBranch("agent/VD-1", "anything")
	if err != nil || got.ID != a.ID {
		t.Fatal(err)
	}
	if _, err := b.AgentByBranch("agent/none", ""); err == nil {
		t.Fatal("found agent for unknown branch")
	}
}

func TestReportDone(t *testing.T) {
	b, _, _, _, clones, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID, Slot: "s1"})
	if _, err := b.ReportDone(a.ID, "did it"); err == nil || !strings.Contains(err.Error(), "push") {
		t.Fatalf("expected push-first error, got %v", err)
	}
	git(t, clones[0], "push", "-u", "origin", "agent/VD-1")
	got, err := b.ReportDone(a.ID, "did it")
	if err != nil || got.Stage != "pushed" {
		t.Fatal(err, got)
	}
	if a, _ = b.AgentByID(a.ID); a.LastMessage != "did it" {
		t.Fatal(a.LastMessage)
	}
}

func TestDocuments(t *testing.T) {
	b, _ := newBoard(t)
	it, _ := b.NewItem("", "x", "", "")
	b.AddDocument(it.ID, "plan", []byte("v1"))
	d, err := b.AddDocument(it.ID, "plan", []byte("v2"))
	if err != nil || d.Version != 2 || filepath.Base(d.Path) != "plan-v2.md" {
		t.Fatal(err, d)
	}
	d, _ = b.Document(d.ID, true)
	if d.Content != "v2" {
		t.Fatal(d.Content)
	}
	if ds, _ := b.Documents(it.ID); len(ds) != 2 {
		t.Fatal(len(ds))
	}
}

func TestAgentStartUntrustedFolder(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	os.WriteFile(f.Path, []byte("#!/bin/sh\necho 'Workspace not trusted. Run `claude` in this folder once.' >&2\nexit 1\n"), 0o755)
	_, err := b.StartAgent(StartOpts{ItemID: it.ID, Slot: "s2"})
	if err == nil || !strings.Contains(err.Error(), "crew slot trust s2") {
		t.Fatalf("err = %v", err)
	}
	if as, _ := b.Agents(); len(as) != 0 {
		t.Fatalf("failed start left agents: %+v", as)
	}
	if s, _ := b.Slot("s2"); s.Agent != "" {
		t.Fatalf("slot still leased: %+v", s)
	}
	if it, _ = b.Item(it.ID); it.AgentID != nil || it.Stage != "queued" {
		t.Fatalf("item: %+v", it)
	}
}

// claude --bg ignores --session-id and picks its own; crew must store that one
func TestAgentStartUsesClaudesSessionID(t *testing.T) {
	b, _, _, _, _, it := startSetup(t)
	a, err := b.StartAgent(StartOpts{ItemID: it.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(a.SessionID, "-0000-4000-8000-000000000000") || a.BgID != a.SessionID[:8] {
		t.Fatalf("session %q bg %q: not the id claude reported", a.SessionID, a.BgID)
	}
	if !hook(t, b, "Stop", a.SessionID, writeTranscript(t, "done")) {
		t.Fatal("Stop hook for the real session id wasn't matched")
	}
}

// An agent that never got a prompt has no conversation: delivery starts it fresh instead
func TestDeliverStartsFreshWithoutConversation(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, err := b.StartAgent(StartOpts{ItemID: it.ID})
	if err != nil {
		t.Fatal(err)
	}
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, ""))
	// no conversation file for its session, and none named after it
	os.RemoveAll(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects"))
	if _, err := b.PostMessage(PostOpts{To: a.Name, Text: "start on the ticket"}); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls(t)
	last := calls[len(calls)-1].Args
	if last[0] != "--bg" || last[1] != "-n" || last[len(last)-1] != "start on the ticket" {
		t.Fatalf("expected a fresh start with the message, got %q", last)
	}
	a2, _ := b.AgentByID(a.ID)
	if a2.SessionID == a.SessionID || a2.Status != StatusWorking {
		t.Fatalf("agent not moved to the new session: %+v", a2)
	}
}

// A new session for an agent (env CREW_AGENT_ID) takes over only once it's used, e.g. after /clear
func TestHookFollowsNewSession(t *testing.T) {
	b, _, _, _, _, it := startSetup(t)
	a, err := b.StartAgent(StartOpts{ItemID: it.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !hook(t, b, "SessionStart", a.SessionID, "") {
		t.Fatal("SessionStart for a known session not handled")
	}
	if a, _ = b.AgentByID(a.ID); a.Status != StatusIdle {
		t.Fatalf("status after SessionStart = %s", a.Status)
	}
	if hook(t, b, "Stop", "11111111-2222-3333-4444-555555555555", "") {
		t.Fatal("unknown session without CREW_AGENT_ID was handled")
	}
	t.Setenv("CREW_AGENT_ID", fmt.Sprint(a.ID))
	if hook(t, b, "SessionStart", "11111111-2222-3333-4444-555555555555", "") {
		t.Fatal("followed a session that only started (it has no conversation yet)")
	}
	if a2, _ := b.AgentByID(a.ID); a2.SessionID != a.SessionID {
		t.Fatal("agent moved off its conversation by a session that merely started")
	}
	if !hook(t, b, "UserPromptSubmit", "11111111-2222-3333-4444-555555555555", "") {
		t.Fatal("new session for the agent not followed once used")
	}
	if a, _ = b.AgentByID(a.ID); a.SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("session = %s", a.SessionID)
	}
}

// The final message lands in the transcript just after the Stop hook starts
func TestStopWaitsForFinalMessage(t *testing.T) {
	b, _, _, _, _, it := startSetup(t)
	a, err := b.StartAgent(StartOpts{ItemID: it.ID})
	if err != nil {
		t.Fatal(err)
	}
	tr := writeTranscript(t, "Ticket read; now checking the code.")
	go func() {
		time.Sleep(150 * time.Millisecond)
		f, _ := os.OpenFile(tr, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString(`{"type":"assistant","message":{"content":[{"type":"text","text":"Here's the plan."}]}}` + "\n")
		f.Close()
	}()
	hook(t, b, "Stop", a.SessionID, tr)
	if a, _ = b.AgentByID(a.ID); a.LastMessage != "Here's the plan." {
		t.Fatalf("last message = %q, want the final one", a.LastMessage)
	}
}

func TestBackgroundAfterNeovim(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	b.SetAttached(a.Name, true)
	b.PostMessage(PostOpts{To: a.Name, Text: "also add a test"}) // attached: waits for the client
	// idle: goes back with the waiting message
	a, err := b.Background(a.Name, false, "")
	if err != nil {
		t.Fatal(err)
	}
	calls := f.Calls(t)
	last := calls[len(calls)-1].Args
	if !slices.Equal(last[:3], []string{"--bg", "--resume", a.SessionID}) || !strings.Contains(last[len(last)-1], "also add a test") {
		t.Fatalf("resume args = %q", last)
	}
	if a.Attached || a.Status != StatusWorking || a.Pending != 0 {
		t.Fatalf("%+v", a)
	}
	// interrupted mid-turn: told to continue
	a, _ = b.Background(a.Name, true, "")
	last = f.Calls(t)[len(f.Calls(t))-1].Args
	if !strings.Contains(last[len(last)-1], "Continue where you left off") {
		t.Fatalf("no continue prompt: %q", last)
	}
}

func TestStaleAndRevive(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "done"))
	if s, _ := b.Sweep(); len(s) != 0 {
		t.Fatal("went stale too early")
	}
	real := b.Now
	b.Now = func() time.Time { return real().Add(3 * time.Hour) }
	stale, _ := b.Sweep()
	b.Now = real
	if len(stale) != 1 || stale[0].Status != StatusStale || stale[0].BgID != "" {
		t.Fatalf("stale = %+v", stale)
	}
	if last := f.Calls(t)[len(f.Calls(t))-1].Args; last[0] != "stop" {
		t.Fatalf("background session not stopped: %q", last)
	}
	n := len(f.Calls(t))
	m, _ := b.PostMessage(PostOpts{To: a.Name, Text: "are you there?"})
	if m.Status != "pending" || len(f.Calls(t)) != n {
		t.Fatal("stale agent was resumed by a message")
	}
	if s, _ := b.Slot("s1"); s.Agent == "" {
		t.Fatal("stale agent lost its slot")
	}
	r, err := b.Revive(a.Name, "")
	if err != nil {
		t.Fatal(err)
	}
	last := f.Calls(t)[len(f.Calls(t))-1].Args
	if r.Status != StatusWorking || last[1] != "--resume" || !strings.Contains(last[len(last)-1], "are you there?") {
		t.Fatalf("revive: %+v %q", r, last)
	}
	if _, err := b.Revive(a.Name, ""); err == nil {
		t.Fatal("revived an agent that's already running")
	}
}

func TestRemoveAgents(t *testing.T) {
	b, _, _, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	if _, err := b.RemoveAgent(a.Name, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("removed a running agent: %v", err)
	}
	if _, err := b.RemoveAgent(a.Name, true); err != nil {
		t.Fatal(err)
	}
	if as, _ := b.Agents(); len(as) != 0 {
		t.Fatal("agent still listed")
	}
	if it, _ = b.Item(it.ID); it.AgentID != nil || it.Stage != "queued" {
		t.Fatalf("item not freed: %+v", it)
	}
	if s, _ := b.Slot("s1"); s.Agent != "" {
		t.Fatal("slot not freed")
	}
	// all: only stopped ones without --force
	a1, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	b.StopAgent(a1.Name)
	removed, err := b.RemoveAll(false)
	if err != nil || len(removed) != 1 {
		t.Fatalf("rm all: %v %d", err, len(removed))
	}
}

// The recorded session lost its conversation (e.g. moved to an unused one): resume the newest
// conversation named after the agent instead of starting over
func TestResumesConversationByName(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	real := a.SessionID
	hook(t, b, "Stop", real, writeTranscript(t, "plan written"))
	b.DB.Exec(`UPDATE agents SET session_id = 'deadbeef-0000-4000-8000-000000000000' WHERE id = ?`, a.ID)
	a, _ = b.AgentByID(a.ID)
	if sid, ok := b.ConversationID(a); !ok || sid != real {
		t.Fatalf("conversation = %q %v, want %s", sid, ok, real)
	}
	if _, err := b.Background(a.Name, false, "go ahead"); err != nil {
		t.Fatal(err)
	}
	last := f.Calls(t)[len(f.Calls(t))-1].Args
	if !slices.Equal(last, withCrew(b, a, []string{"--bg", "--resume", real}, "--permission-mode", "auto", "go ahead")) {
		t.Fatalf("resumed %q, want the named conversation", last)
	}
}

// claude --bg returns before the session runs; one that then fails must not count as working
func TestBackgroundFailureIsReported(t *testing.T) {
	b, _, _, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "done"))
	t.Setenv("FAKE_BG_FAIL", "source session not found")
	if _, err := b.Background(a.Name, false, "go"); err == nil || !strings.Contains(err.Error(), "source session not found") {
		t.Fatalf("err = %v", err)
	}
	if a2, _ := b.AgentByID(a.ID); a2.Status == StatusWorking {
		t.Fatal("failed background session shown as working")
	}
	m, _ := b.PostMessage(PostOpts{To: a.Name, Text: "hello"})
	if m.Status != "pending" {
		t.Fatalf("message to a failed resume marked %s", m.Status)
	}
}

func TestWorkerPermissionModeOverride(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	t.Setenv("CREW_WORKER_PERMISSION_MODE", "acceptEdits")
	if _, err := b.StartAgent(StartOpts{ItemID: it.ID}); err != nil {
		t.Fatal(err)
	}
	args := f.Calls(t)[0].Args
	if !slices.Contains(args, "acceptEdits") {
		t.Fatalf("args = %q", args)
	}
}

// Each slot's agents may read and edit their own slot, and are denied every other slot
func TestSlotPermissions(t *testing.T) {
	b, _ := newBoard(t)
	dir := t.TempDir()
	for _, d := range []string{"one", "two"} {
		git(t, dir, "init", "-q", d)
	}
	os.MkdirAll(dir+"/one/.claude", 0o755)
	os.WriteFile(dir+"/one/.claude/settings.local.json", []byte(`{"model":"opus","permissions":{"allow":["Bash(make test)"]}}`), 0o644)
	b.AddSlot("one", dir+"/one", -1)
	b.AddSlot("two", dir+"/two", -1)
	read := func(slot string) (allow, deny []string, other map[string]any) {
		raw, err := os.ReadFile(dir + "/" + slot + "/.claude/settings.local.json")
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Model       string `json:"model"`
			Permissions struct{ Allow, Deny []string }
		}
		json.Unmarshal(raw, &s)
		other = map[string]any{"model": s.Model}
		return s.Permissions.Allow, s.Permissions.Deny, other
	}
	abs := func(p string) string { return "//" + strings.TrimPrefix(dir, "/") + "/" + p + "/**" }
	allow, deny, other := read("one")
	if !slices.Contains(allow, "Edit("+abs("one")+")") || !slices.Contains(allow, "Read("+abs("one")+")") {
		t.Fatalf("one allow = %q", allow)
	}
	if !slices.Contains(deny, "Read("+abs("two")+")") || slices.Contains(deny, "Read("+abs("one")+")") {
		t.Fatalf("one deny = %q", deny)
	}
	if !slices.Contains(deny, "Bash(git push --force*)") || !slices.Contains(allow, "Bash(git push origin agent/*)") {
		t.Fatalf("git rules missing: allow %q deny %q", allow, deny)
	}
	if !slices.Contains(allow, "mcp__crew__*") {
		t.Fatalf("crew's MCP tools not allowed: %q", allow)
	}
	if !slices.Contains(allow, "Bash(make test)") || other["model"] != "opus" {
		t.Fatal("dropped settings that crew didn't write")
	}
	if _, deny, _ = read("two"); !slices.Contains(deny, "Read("+abs("one")+")") {
		t.Fatalf("two deny = %q", deny)
	}
	// rewriting doesn't duplicate rules, and the file stays out of git
	b.WriteSlotPermissions()
	if raw, _ := os.ReadFile(dir + "/one/.claude/settings.local.json"); !strings.Contains(string(raw), `"bgIsolation": "none"`) {
		t.Fatal("worktree isolation not turned off")
	}
	if allow, _, _ = read("one"); len(allow) != 3+len(gitAllow)+len(mcpAllow) {
		t.Fatalf("rules duplicated: %q", allow)
	}
	if st := git(t, dir+"/one", "status", "--porcelain", "--untracked-files=all"); strings.Contains(st, "settings.local.json") {
		t.Fatalf("permissions file shows up in git status: %s", st)
	}
}

// Claude's idle reminder isn't a request; a message to an agent waiting on a human is delivered
func TestNeedsInputAndIdleReminder(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "Decisions I need from you: ..."))
	hookMsg := func(msg string) {
		raw, _ := json.Marshal(map[string]string{"hook_event_name": "Notification", "session_id": a.SessionID, "message": msg})
		if _, err := b.HandleHook(raw); err != nil {
			t.Fatal(err)
		}
	}
	hookMsg("Claude is waiting for your input")
	if a2, _ := b.AgentByID(a.ID); a2.Status != StatusIdle {
		t.Fatalf("idle reminder changed status to %s", a2.Status)
	}
	hookMsg("Claude needs your permission to use Bash")
	if a2, _ := b.AgentByID(a.ID); a2.Status != StatusNeedsInput {
		t.Fatalf("permission prompt: status %s", a2.Status)
	}
	m, err := b.PostMessage(PostOpts{To: a.Name, Text: "1: yes. 2: no."})
	if err != nil || m.Status != "delivered" {
		t.Fatalf("message to a needs-input agent: %v %+v", err, m)
	}
	if last := f.Calls(t)[len(f.Calls(t))-1].Args; last[len(last)-1] != "1: yes. 2: no." {
		t.Fatalf("resume args %q", last)
	}
}

// Comments made on a commit, or on the old side of a diff, say so in the message to the agent
func TestCommentOnCommitAndOldSide(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "done"))
	added, err := b.AddComments([]Comment{
		{Branch: a.Branch, File: "a.go", StartLine: 3, EndLine: 3, Excerpt: []string{"x := 1"}, Text: "rename", Commit: "a1b2c3d4e5f60718", Side: "new"},
		{Branch: a.Branch, File: "b.go", StartLine: 7, EndLine: 7, Excerpt: []string{"old()"}, Text: "why was this removed?", Commit: "a1b2c3d4e5f60718", Side: "old"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendComments(added, "you"); err != nil {
		t.Fatal(err)
	}
	args := f.Calls(t)[len(f.Calls(t))-1].Args
	msg := args[len(args)-1]
	if !strings.Contains(msg, "`a.go:3` (in commit a1b2c3d4e5)") || !strings.Contains(msg, "`b.go:7` (before the change, in commit a1b2c3d4e5)") {
		t.Fatalf("message:\n%s", msg)
	}
	cs, _ := b.Comments(CommentFilter{Branch: a.Branch})
	if len(cs) != 2 || cs[1].Side != "old" || cs[0].Commit == "" {
		t.Fatalf("stored: %+v", cs)
	}
}

// Starting an agent always gives it the ticket; --prompt adds to it
func TestStartGivesTheTicket(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	tk, _ := b.AddTicket("", "", "Write a PRD for search.\nCover the benefits.", nil)
	it2, err := b.NewItem(fmt.Sprint(tk.ID), "", "", "agent/prd")
	if err != nil {
		t.Fatal(err)
	}
	b.MoveItem(it2.ID, "queued", false)
	_ = it
	if _, err := b.StartAgent(StartOpts{ItemID: it2.ID, Prompt: "Plan first."}); err != nil {
		t.Fatal(err)
	}
	args := f.Calls(t)[len(f.Calls(t))-1].Args
	p := args[len(args)-1]
	if !strings.Contains(p, "Write a PRD for search.") || !strings.Contains(p, "Cover the benefits.") || !strings.HasSuffix(p, "Plan first.") {
		t.Fatalf("first prompt:\n%s", p)
	}
}

// Starting an item that already has an agent gives that agent the work again instead of failing
func TestStartAgainResends(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "idle now"))
	a2, m, err := b.StartOrResend(StartOpts{ItemID: it.ID, Prompt: "Start with the tests."})
	if err != nil || a2.ID != a.ID || m == nil || m.Status != "delivered" {
		t.Fatalf("resend to idle agent: %v %+v %+v", err, a2, m)
	}
	args := f.Calls(t)[len(f.Calls(t))-1].Args
	if p := args[len(args)-1]; !strings.Contains(p, "Jira ticket: VD-1") || !strings.HasSuffix(p, "Start with the tests.") {
		t.Fatalf("resent prompt: %q", p)
	}
	// busy: waits for the end of its turn
	hook(t, b, "UserPromptSubmit", a.SessionID, "")
	if _, m, _ = b.StartOrResend(StartOpts{ItemID: it.ID}); m == nil || m.Status != "pending" {
		t.Fatalf("resend to busy agent: %+v", m)
	}
	if as, _ := b.Agents(); len(as) != 1 {
		t.Fatalf("started a second agent: %d", len(as))
	}
}

// After /clear in a background session (Claude's daemon doesn't pass crew's env to hooks), the
// board follows the agent by the session's name, and resumes the newer conversation
func TestFollowAfterClearByName(t *testing.T) {
	b, _, _, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	os.Unsetenv("CREW_AGENT_ID")
	pdir := projectDir(a.Dir)
	newSID := "7e57c1ea-0000-4000-8000-000000000000"
	tr := filepath.Join(pdir, newSID+".jsonl")
	time.Sleep(20 * time.Millisecond)
	os.WriteFile(tr, []byte(`{"type":"custom-title","customTitle":"`+a.Name+`","sessionId":"`+newSID+`"}`+"\n"), 0o644)
	if sid, _ := b.ConversationID(a); sid != newSID {
		t.Fatalf("conversation = %s, want the newer one after /clear", sid)
	}
	raw, _ := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": newSID, "transcript_path": tr, "cwd": filepath.Join(a.Dir, "sub")})
	if ok, err := b.HandleHook(raw); !ok || err != nil {
		t.Fatalf("not followed by name: %v %v", ok, err)
	}
	if a2, _ := b.AgentByID(a.ID); a2.SessionID != newSID || a2.Status != StatusWorking {
		t.Fatalf("agent: %+v", a2)
	}
	// someone else's session (other name, or another folder) is left alone
	other := filepath.Join(pdir, "0ther000-0000-4000-8000-000000000000.jsonl")
	os.WriteFile(other, []byte(`{"type":"custom-title","customTitle":"Someone Else"}`+"\n"), 0o644)
	raw, _ = json.Marshal(map[string]string{"hook_event_name": "Stop", "session_id": "0ther000-0000-4000-8000-000000000000", "transcript_path": other, "cwd": a.Dir})
	if ok, _ := b.HandleHook(raw); ok {
		t.Fatal("followed a session with another name")
	}
	raw, _ = json.Marshal(map[string]string{"hook_event_name": "Stop", "session_id": "elsewh00-0000-4000-8000-000000000000", "transcript_path": tr, "cwd": "/somewhere/else"})
	if ok, _ := b.HandleHook(raw); ok {
		t.Fatal("followed a session in another folder")
	}
}

// A running session gets messages through the mailman: no stop, no resume, same process
func TestMailmanDelivers(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "done"))
	socks := t.TempDir()
	t.Setenv("CREW_CC_SOCKS", socks)
	t.Setenv("FAKE_LIVE_PID", "4242")
	os.WriteFile(filepath.Join(socks, "4242.sock"), nil, 0o644)
	n := len(f.Calls(t))
	m, err := b.PostMessage(PostOpts{To: a.Name, Text: "please add tests"})
	if err != nil || m.Status != "delivered" {
		t.Fatalf("%v %+v", err, m)
	}
	calls := f.Calls(t)[n:]
	var mailman *FakeCall
	for i, c := range calls {
		if c.Args[0] == "stop" || slices.Contains(c.Args, "--resume") {
			t.Fatalf("session restarted: %q", c.Args)
		}
		if c.Args[0] == "-p" {
			mailman = &calls[i]
		}
	}
	if mailman == nil || !slices.Contains(mailman.Args, "haiku") || !slices.Contains(mailman.Args, "auto") {
		t.Fatalf("mailman call: %+v", mailman)
	}
	stdin, _ := os.ReadFile(filepath.Join(f.Dir, fmt.Sprint(len(f.Calls(t))), "stdin"))
	if !strings.Contains(string(stdin), "uds:"+filepath.Join(socks, "4242.sock")) || !strings.Contains(string(stdin), "please add tests") ||
		!strings.Contains(string(stdin), "don't reply to it with SendMessage") {
		t.Fatalf("mailman prompt:\n%s", stdin)
	}
	if a2, _ := b.AgentByID(a.ID); a2.Status != StatusWorking || a2.SessionID != a.SessionID {
		t.Fatalf("agent: %+v", a2)
	}
}

// If the mailman can't send it, crew resumes the session with the message instead
func TestMailmanFallsBackToResume(t *testing.T) {
	b, _, f, _, _, it := startSetup(t)
	a, _ := b.StartAgent(StartOpts{ItemID: it.ID})
	hook(t, b, "Stop", a.SessionID, writeTranscript(t, "done"))
	socks := t.TempDir()
	t.Setenv("CREW_CC_SOCKS", socks)
	t.Setenv("FAKE_LIVE_PID", "4242")
	os.WriteFile(filepath.Join(socks, "4242.sock"), nil, 0o644)
	t.Setenv("FAKE_MAIL_REPLY", "FAILED: no such session")
	m, err := b.PostMessage(PostOpts{To: a.Name, Text: "please add tests"})
	if err != nil || m.Status != "delivered" {
		t.Fatalf("%v %+v", err, m)
	}
	last := f.Calls(t)[len(f.Calls(t))-1].Args
	if !slices.Contains(last, "--resume") || last[len(last)-1] != "please add tests" {
		t.Fatalf("no resume fallback: %q", last)
	}
	evs, _ := b.Events(0, 1000)
	found := false
	for _, e := range evs {
		if e.Type == "msg.deliver" && strings.Contains(string(e.Data), "mailman_error") && strings.Contains(string(e.Data), `"via":"resume"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("fallback not recorded in the event")
	}
}
