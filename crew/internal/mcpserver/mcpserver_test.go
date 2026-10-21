package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xdewald/dotfiles/crew/internal/board"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestReportDone(t *testing.T) {
	t.Setenv("CREW_NO_NOTIFY", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	root := t.TempDir()
	fake := filepath.Join(root, "claude")
	os.WriteFile(fake, []byte("#!/bin/sh\nif [ \"$1\" = agents ]; then echo '[{\"sessionId\":\"abc12345-0000-4000-8000-000000000000\",\"kind\":\"background\",\"status\":\"idle\"}]'; exit 0; fi\necho \"backgrounded · abc12345 · agent\"\n"), 0o755)
	t.Setenv("CREW_CLAUDE", fake)
	origin, clone := filepath.Join(root, "origin.git"), filepath.Join(root, "slot")
	git(t, root, "init", "--bare", "-b", "main", origin)
	git(t, root, "clone", origin, clone)
	os.WriteFile(filepath.Join(clone, "f"), []byte("x"), 0o644)
	git(t, clone, "add", ".")
	git(t, clone, "commit", "-m", "init")
	git(t, clone, "push", "origin", "HEAD:main")

	b, err := board.Open(filepath.Join(root, "home"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.AddSlot("s1", clone, 0)
	it, _ := b.NewItem("", "thing", "", "agent/T-1")
	b.MoveItem(it.ID, "queued", false)
	a, err := b.StartAgent(board.StartOpts{ItemID: it.ID})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := New(b, func() string { return fmt.Sprint(a.ID) })
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	call := func(name string, args any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}
	text := func(r *mcp.CallToolResult) string {
		var sb strings.Builder
		for _, c := range r.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		return sb.String()
	}

	res := call("report_done", map[string]any{"summary": "done"})
	if !res.IsError || !strings.Contains(text(res), "push it first") {
		t.Fatalf("expected push-first error, got %v %s", res.IsError, text(res))
	}
	git(t, clone, "push", "origin", "agent/T-1")
	res = call("report_done", map[string]any{"summary": "done"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if it, _ = b.Item(it.ID); it.Stage != "pushed" {
		t.Fatal(it.Stage)
	}

	// the other tools answer and validate their output schemas
	for name, args := range map[string]any{
		"board_status":  map[string]any{},
		"list_items":    map[string]any{"stage": "pushed"},
		"show_item":     map[string]any{"id": it.ID},
		"list_messages": map[string]any{},
		"list_comments": map[string]any{"branch": "agent/T-1"},
		"add_comment":   map[string]any{"branch": "agent/T-1", "file": "f", "start_line": 1, "excerpt": []string{"x"}, "text": "hm"},
		"post_message":  map[string]any{"item_id": it.ID, "text": "hi"},
		"move_item":     map[string]any{"id": it.ID, "stage": "reviewing"},
	} {
		if r := call(name, args); r.IsError {
			t.Errorf("%s: %s", name, text(r))
		}
	}
	var out struct {
		Comments []board.Comment `json:"comments"`
	}
	j, _ := json.Marshal(call("list_comments", map[string]any{"item_id": it.ID}).StructuredContent)
	json.Unmarshal(j, &out)
	if len(out.Comments) != 1 || out.Comments[0].Author != a.Name {
		t.Fatalf("%s", j)
	}
}
