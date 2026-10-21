package board

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
	want := []string{"--bg", "--session-id", a.SessionID, "-n", a.Name, "--permission-mode", "acceptEdits"}
	if !slices.Equal(c.Args, want) {
		t.Fatalf("args = %q, want %q", c.Args, want)
	}
	if c.Cwd != clones[1] {
		t.Fatalf("cwd = %s", c.Cwd)
	}
	slug := strings.ToLower(strings.ReplaceAll(a.Name, " ", "-"))
	for k, v := range map[string]string{
		"CREW_PORT_OFFSET": "100", "CREW_PROCESS_PREFIX": slug + "-", "CREW_SESSION_ID": a.SessionID,
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
	if !slices.Equal(calls[2].Args, []string{"--bg", "--resume", a.SessionID, "also fix Y"}) {
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
	if last := c[len(c)-1].Args; last[1] != "--resume" || last[3] != "wake up" {
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
	prompt := c[len(c)-1].Args[3]
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

func TestBgID(t *testing.T) {
	if got := bgID("8798868b-f0d7-4c3c-bbb5-9eed9a2c927c"); got != "8798868b" {
		t.Errorf("got %q", got)
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
