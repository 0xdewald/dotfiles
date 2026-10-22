package board

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
		"CREW_NO_NOTIFY": "1",
	} {
		os.Setenv(k, v)
	}
	os.Exit(m.Run())
}

// recExec runs real processes but records notifications.
type recExec struct {
	Exec
	mu       sync.Mutex
	notified []string
}

func (r *recExec) Notify(title, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notified = append(r.notified, title+": "+msg)
}

// Fake is a fake claude binary recording each call to its own directory.
type Fake struct {
	Path, Dir string
}

// Like the real claude: --bg picks the session id itself (printing "backgrounded · <short>"),
// --bg --resume keeps it, and `agents --json` lists background sessions.
const fakeScript = `#!/bin/sh
d="@DIR@"
if [ "$1" = "agents" ]; then
  printf '['; sep=''
  [ -f "$d.bgs" ] && while read id cwd st; do printf '%s{"sessionId":"%s-0000-4000-8000-000000000000","cwd":"%s","kind":"background","status":%s,"pid":%s}' "$sep" "$id" "$cwd" "$st" "${FAKE_LIVE_PID:-0}"; sep=','; done < "$d.bgs"
  printf ']\n'; exit 0
fi
if [ "$1" = "-p" ]; then # the mailman: prompt on stdin
  n=$(ls "$d" | wc -l | tr -d ' '); n=$((n + 1)); mkdir -p "$d/$n"
  for a in "$@"; do printf '%s\0' "$a"; done > "$d/$n/args"; env | grep '^CREW_' | sort > "$d/$n/env"; pwd -P > "$d/$n/cwd"
  cat > "$d/$n/stdin"; echo "${FAKE_MAIL_REPLY:-SENT}"; exit 0
fi
n=$(ls "$d" | wc -l | tr -d ' ')
n=$((n + 1))
mkdir -p "$d/$n"
for a in "$@"; do printf '%s\0' "$a"; done > "$d/$n/args"
env | grep '^CREW_' | sort > "$d/$n/env"
pwd -P > "$d/$n/cwd"
if [ "$1" = "--bg" ]; then
  if [ "$2" = "--resume" ]; then id=$(echo "$3" | cut -c1-8); else id=$(printf '%08x' $((2684354560 + n))); fi
  # one entry per session id, like claude agents
  [ -f "$d.bgs" ] && grep -v "^$id " "$d.bgs" > "$d.bgs.tmp"; [ -f "$d.bgs.tmp" ] && mv "$d.bgs.tmp" "$d.bgs"
  if [ -n "$FAKE_BG_FAIL" ]; then
    echo "$id $(pwd -P) null" >> "$d.bgs"
    mkdir -p "$CLAUDE_CONFIG_DIR/jobs/$id"
    printf '{"state":"failed","detail":"%s"}' "$FAKE_BG_FAIL" > "$CLAUDE_CONFIG_DIR/jobs/$id/state.json"
  else
    echo "$id $(pwd -P) \"idle\"" >> "$d.bgs"
    # like claude: the conversation lives in <config>/projects/<cwd>/<session>.jsonl, named by -n
    name=""; prev=""; for a in "$@"; do [ "$prev" = "-n" ] && name="$a"; prev="$a"; done
    p="$CLAUDE_CONFIG_DIR/projects/$(pwd -P | sed 's/[^a-zA-Z0-9]/-/g')"; mkdir -p "$p"
    f="$p/$id-0000-4000-8000-000000000000.jsonl"
    [ -f "$f" ] || printf '{"type":"custom-title","customTitle":"%s"}\n' "$name" > "$f"
  fi
  echo "backgrounded · $id · agent (idle — send a prompt to start)"
fi
`

// FakeCall is one recorded claude invocation.
type FakeCall struct {
	Args []string
	Env  map[string]string
	Cwd  string
}

func newFake(t *testing.T) *Fake {
	t.Helper()
	root := t.TempDir()
	f := &Fake{Path: filepath.Join(root, "claude"), Dir: filepath.Join(root, "calls")}
	os.MkdirAll(f.Dir, 0o755)
	if err := os.WriteFile(f.Path, []byte(strings.ReplaceAll(fakeScript, "@DIR@", f.Dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREW_CLAUDE", f.Path)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude-home"))
	return f
}

func (f *Fake) Calls(t *testing.T) []FakeCall {
	t.Helper()
	ents, _ := os.ReadDir(f.Dir)
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) < len(names[j])
		}
		return names[i] < names[j]
	})
	var out []FakeCall
	for _, n := range names {
		var c FakeCall
		a, _ := os.ReadFile(filepath.Join(f.Dir, n, "args"))
		c.Args = strings.Split(strings.TrimSuffix(string(a), "\x00"), "\x00")
		c.Env = map[string]string{}
		e, _ := os.ReadFile(filepath.Join(f.Dir, n, "env"))
		for _, l := range strings.Split(strings.TrimSpace(string(e)), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				c.Env[k] = v
			}
		}
		cwd, _ := os.ReadFile(filepath.Join(f.Dir, n, "cwd"))
		c.Cwd = strings.TrimSpace(string(cwd))
		out = append(out, c)
	}
	return out
}

func newBoard(t *testing.T) (*Board, *recExec) {
	t.Helper()
	b, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	r := &recExec{Exec: DefaultExec()}
	b.Exec = r
	return b, r
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoSetup makes a bare origin with one commit and n clones; returns origin and clone paths.
func repoSetup(t *testing.T, n int) (string, []string) {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "--bare", "-b", "main", origin)
	seed := filepath.Join(root, "seed")
	git(t, root, "clone", origin, seed)
	os.WriteFile(filepath.Join(seed, "README"), []byte("hi\n"), 0o644)
	git(t, seed, "add", ".")
	git(t, seed, "commit", "-m", "init")
	git(t, seed, "push", "origin", "HEAD:main")
	var clones []string
	for i := range n {
		c := filepath.Join(root, "slot"+string(rune('a'+i)))
		git(t, root, "clone", origin, c)
		clones = append(clones, c)
	}
	return origin, clones
}

// withCrew is head, then the crew MCP config and role instructions every start and resume
// carries, then tail.
func withCrew(b *Board, a *Agent, head []string, tail ...string) []string {
	out := append(slices.Clone(head), "--mcp-config", b.MCPConfigPath(a.ID))
	if p := rolePrompt(a); p != "" {
		out = append(out, "--append-system-prompt", p)
	}
	return append(out, tail...)
}
