// Command crew is the agent crew work board: CLI, hook handler, HTTP API and MCP server.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/0xdewald/dotfiles/crew/internal/board"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `crew: local work board for a crew of Claude Code agents

usage: crew <command> [args] [--json]

  init
  status
  ticket add [--jira KEY] [--text TEXT|-] [free text...] | show ID|KEY | ls
  item   new --ticket ID [--title T] [--repo R] [--branch B] | ls [--stage S] | show ID | move ID STAGE [--force]
  slot   add NAME PATH [--clone URL] [--port-offset N] | ls | trust NAME | lease NAME --agent ID | release NAME
  agent  start ITEM [--slot S] [--role R] [--model M] [--name N] [--prompt P]
         ls | show ID|NAME | show --session SID | show --branch B [--repo R]
         stop ID | background ID [--continue] [--prompt P] | revive ID [--prompt P]
         rm ID|all [--force] | attach-mark ID on|off
  msg    post (--to AGENT | --item ID) [--kind note|work|review] [--from F] TEXT|- | ls [--to A] [--status S] | ack ID
  deliver AGENT
  comment add [flags] | add --stdin-json [--send] | ls [--item] [--branch] [--repo] [--status] | resolve ID
  doc    add ITEM --kind KIND FILE|- | ls ITEM | show ID
  event  --hook (reads a Claude Code hook payload on stdin) | ls [--since N]
  serve  [--addr 127.0.0.1:7767]
  mcp    (MCP server on stdio)

env: CREW_HOME, CREW_CLAUDE, CREW_NO_NOTIFY, CREW_CONFIG, CREW_STALE_AFTER (default 2h), JIRA_BASE_URL, JIRA_EMAIL, JIRA_API_TOKEN
`

// app carries per-invocation state.
type app struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	json           bool
	b              *board.Board
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	loadConfig()
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr}
	args = a.stripJSON(args)
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if args[0] == "event" && hasArg(args, "--hook") {
		return a.hook()
	}
	b, err := board.Open(board.DefaultHome())
	if err != nil {
		fmt.Fprintln(stderr, "crew:", err)
		return 1
	}
	defer b.Close()
	a.b = b
	// agents idle for too long go stale; checked whenever crew runs (crew serve also checks every minute)
	if args[0] != "mcp" && args[0] != "serve" {
		b.Sweep()
	}
	if err := a.dispatch(args); err != nil {
		fmt.Fprintln(stderr, "crew:", err)
		var ue usageError
		if errors.As(err, &ue) {
			return 2
		}
		return 1
	}
	return 0
}

func (a *app) stripJSON(args []string) []string {
	out := args[:0:0]
	for i, x := range args {
		if x == "--" {
			return append(out, args[i:]...)
		}
		if x == "--json" || x == "-json" {
			a.json = true
			continue
		}
		out = append(out, x)
	}
	return out
}

func hasArg(args []string, want string) bool {
	for _, x := range args {
		if x == want || x == strings.TrimPrefix(want, "-") {
			return true
		}
	}
	return false
}

// hook handles `crew event --hook`: silent on stdout, always exit 0.
func (a *app) hook() int {
	home := board.DefaultHome()
	if _, err := os.Stat(filepath.Join(home, "crew.db")); err != nil {
		return 0
	}
	raw, err := io.ReadAll(a.stdin)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return 0
	}
	b, err := board.Open(home)
	if err != nil {
		fmt.Fprintln(a.stderr, "crew:", err)
		return 0
	}
	defer b.Close()
	if _, err := b.HandleHook(raw); err != nil {
		fmt.Fprintln(a.stderr, "crew:", err)
	}
	return 0
}

func (a *app) dispatch(args []string) error {
	cmd, rest := args[0], args[1:]
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	switch cmd {
	case "init":
		return a.out(map[string]string{"home": a.b.Home, "db": filepath.Join(a.b.Home, "crew.db")}, func() {
			fmt.Fprintln(a.stdout, "crew board ready at", a.b.Home)
		})
	case "status":
		return a.status()
	case "ticket":
		return a.ticket(sub, tail(rest))
	case "item":
		return a.item(sub, tail(rest))
	case "slot":
		return a.slot(sub, tail(rest))
	case "agent":
		return a.agent(sub, tail(rest))
	case "msg":
		return a.msg(sub, tail(rest))
	case "deliver":
		pos, err := parse(flag.NewFlagSet("deliver", flag.ContinueOnError), rest, 1)
		if err != nil {
			return err
		}
		n, err := a.b.Deliver(pos[0])
		if err != nil {
			return err
		}
		return a.out(map[string]int{"delivered": n}, func() { fmt.Fprintf(a.stdout, "delivered %d message(s)\n", n) })
	case "comment":
		return a.comment(sub, tail(rest))
	case "doc":
		return a.doc(sub, tail(rest))
	case "event":
		return a.events(sub, tail(rest))
	case "serve":
		return a.serve(rest)
	case "mcp":
		return a.mcp()
	}
	return usageError{"unknown command " + cmd + " (crew help)"}
}

func tail(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s[1:]
}

// parse parses flags interspersed with positionals and checks there are at
// least min positionals.
func parse(fs *flag.FlagSet, args []string, min int) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usageError{fs.Name() + ": " + err.Error()}
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		if args[0] == "--" {
			pos = append(pos, args[1:]...)
			break
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
	if len(pos) < min {
		return nil, usageError{fs.Name() + ": missing arguments (crew help)"}
	}
	return pos, nil
}

// out prints v as JSON with --json, else runs human.
func (a *app) out(v any, human func()) error {
	if a.json {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	human()
	return nil
}

func (a *app) table(header string, rows func(w io.Writer)) {
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, header)
	rows(w)
	w.Flush()
}

// textArg joins positionals; a lone "-" reads stdin.
func (a *app) textArg(pos []string) (string, error) {
	if len(pos) == 1 && pos[0] == "-" {
		b, err := io.ReadAll(a.stdin)
		return strings.TrimRight(string(b), "\n"), err
	}
	return strings.Join(pos, " "), nil
}

// loadConfig reads KEY=VALUE lines from the crew env file into the process
// env, without overriding variables already set.
func loadConfig() {
	path := os.Getenv("CREW_CONFIG")
	if path == "" {
		dir := os.Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".config")
		}
		path = filepath.Join(dir, "crew", "env")
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
