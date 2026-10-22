package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/0xdewald/dotfiles/crew/internal/board"
	"github.com/0xdewald/dotfiles/crew/internal/jira"
)

func atoi(s, what string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, usageError{fmt.Sprintf("%s must be a number, got %q", what, s)}
	}
	return n, nil
}

func (a *app) status() error {
	s, err := a.b.Status()
	if err != nil {
		return err
	}
	return a.out(s, func() {
		for _, st := range board.Stages {
			if n := s.Stages[st]; n > 0 {
				fmt.Fprintf(a.stdout, "%-13s %d\n", st, n)
			}
		}
		a.printAgents(s.Agents)
	})
}

// ---- tickets

func (a *app) ticket(sub string, args []string) error {
	switch sub {
	case "add":
		fs := flag.NewFlagSet("ticket add", flag.ContinueOnError)
		key := fs.String("jira", "", "Jira key")
		text := fs.String("text", "", "ticket text, or - for stdin")
		pos, err := parse(fs, args, 0)
		if err != nil {
			return err
		}
		body, err := a.textArg(pos)
		if err != nil {
			return err
		}
		if *text == "-" {
			if *text, err = a.textArg([]string{"-"}); err != nil {
				return err
			}
		}
		body = strings.TrimSpace(strings.Join(nonEmpty(body, *text), "\n\n"))
		if *key == "" {
			*key = jira.ExtractKey(body)
		}
		if *key == "" && body == "" {
			return usageError{"ticket add: give a Jira key or some text"}
		}
		title, raw := "", []byte(nil)
		if *key != "" {
			if c := jira.FromEnv(os.Getenv); c != nil {
				if title, raw, err = c.Fetch(*key); err != nil {
					fmt.Fprintln(a.stderr, "crew: warning:", err, "(stored the key only)")
				}
			} else {
				fmt.Fprintln(a.stderr, "crew: warning: JIRA_BASE_URL/JIRA_EMAIL/JIRA_API_TOKEN not set; stored the key only")
			}
		}
		if title == "" && *key == "" {
			title = firstLine(body)
		}
		t, err := a.b.AddTicket(*key, title, body, raw)
		if err != nil {
			return err
		}
		return a.out(t, func() { fmt.Fprintf(a.stdout, "ticket %d %s %s\n", t.ID, orDash(t.JiraKey), t.Title) })
	case "show":
		pos, err := parse(flag.NewFlagSet("ticket show", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		t, err := a.b.Ticket(pos[0])
		if err != nil {
			return err
		}
		return a.out(t, func() {
			fmt.Fprintf(a.stdout, "ticket %d  %s  %s\ncreated %s\n\n%s\n", t.ID, orDash(t.JiraKey), t.Title, t.Created, t.Text)
		})
	case "ls":
		if _, err := parse(flag.NewFlagSet("ticket ls", flag.ContinueOnError), args, 0); err != nil {
			return err
		}
		ts, err := a.b.Tickets()
		if err != nil {
			return err
		}
		return a.out(ts, func() {
			a.table("ID\tKEY\tTITLE", func(w io.Writer) {
				for _, t := range ts {
					fmt.Fprintf(w, "%d\t%s\t%s\n", t.ID, orDash(t.JiraKey), short(orDash(t.Title), 60))
				}
			})
		})
	}
	return usageError{"ticket: add | show | ls"}
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return short(s, 80)
}

// ---- items

type itemDetail struct {
	*board.Item
	Ticket    *board.Ticket     `json:"ticket"`
	Documents []*board.Document `json:"documents"`
	Comments  []*board.Comment  `json:"comments"`
	Messages  []*board.Message  `json:"messages"`
}

func (a *app) itemDetail(id int64) (*itemDetail, error) {
	it, err := a.b.Item(id)
	if err != nil {
		return nil, err
	}
	d := &itemDetail{Item: it}
	if it.TicketID != nil {
		d.Ticket, _ = a.b.Ticket(strconv.FormatInt(*it.TicketID, 10))
	}
	if d.Documents, err = a.b.Documents(id); err != nil {
		return nil, err
	}
	if d.Comments, err = a.b.Comments(board.CommentFilter{ItemID: id}); err != nil {
		return nil, err
	}
	msgs, err := a.b.Messages("", "")
	if err != nil {
		return nil, err
	}
	d.Messages = []*board.Message{}
	for _, m := range msgs {
		if m.ItemID != nil && *m.ItemID == id {
			d.Messages = append(d.Messages, m)
		}
	}
	return d, nil
}

func (a *app) item(sub string, args []string) error {
	switch sub {
	case "new":
		fs := flag.NewFlagSet("item new", flag.ContinueOnError)
		ticket := fs.String("ticket", "", "ticket id or Jira key")
		title := fs.String("title", "", "title (default: ticket title)")
		repo := fs.String("repo", "", "repo name (subdir of the slot)")
		branch := fs.String("branch", "", "branch (default agent/<key|id>)")
		if _, err := parse(fs, args, 0); err != nil {
			return err
		}
		if *ticket == "" && *title == "" {
			return usageError{"item new: --ticket or --title required"}
		}
		it, err := a.b.NewItem(*ticket, *title, *repo, *branch)
		if err != nil {
			return err
		}
		return a.out(it, func() { fmt.Fprintf(a.stdout, "item %d %s on %s\n", it.ID, it.Title, it.Branch) })
	case "ls":
		fs := flag.NewFlagSet("item ls", flag.ContinueOnError)
		stage := fs.String("stage", "", "filter by stage")
		if _, err := parse(fs, args, 0); err != nil {
			return err
		}
		items, err := a.b.Items(*stage)
		if err != nil {
			return err
		}
		return a.out(items, func() {
			a.table("ID\tSTAGE\tKEY\tBRANCH\tAGENT\tTITLE", func(w io.Writer) {
				for _, i := range items {
					fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", i.ID, i.Stage, orDash(i.JiraKey), i.Branch, orDash(i.Agent), short(i.Title, 50))
				}
			})
		})
	case "show":
		pos, err := parse(flag.NewFlagSet("item show", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "item id")
		if err != nil {
			return err
		}
		d, err := a.itemDetail(id)
		if err != nil {
			return err
		}
		return a.out(d, func() {
			fmt.Fprintf(a.stdout, "item %d  %s\nstage   %s (rounds %d)\nbranch  %s %s\nagent   %s\n", d.ID, d.Title, d.Stage, d.Rounds, d.Branch, d.Repo, orDash(d.Agent))
			if d.Ticket != nil {
				fmt.Fprintf(a.stdout, "ticket  %d %s\n", d.Ticket.ID, orDash(d.Ticket.JiraKey))
			}
			for _, doc := range d.Documents {
				fmt.Fprintf(a.stdout, "doc     %d %s v%d %s\n", doc.ID, doc.Kind, doc.Version, doc.Path)
			}
			fmt.Fprintf(a.stdout, "comments %d, messages %d\n", len(d.Comments), len(d.Messages))
		})
	case "move":
		fs := flag.NewFlagSet("item move", flag.ContinueOnError)
		force := fs.Bool("force", false, "skip lifecycle checks")
		pos, err := parse(fs, args, 2)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "item id")
		if err != nil {
			return err
		}
		it, err := a.b.MoveItem(id, pos[1], *force)
		if err != nil {
			return err
		}
		return a.out(it, func() { fmt.Fprintf(a.stdout, "item %d → %s\n", it.ID, it.Stage) })
	}
	return usageError{"item: new | ls | show | move"}
}

// ---- slots

func (a *app) slot(sub string, args []string) error {
	switch sub {
	case "add":
		fs := flag.NewFlagSet("slot add", flag.ContinueOnError)
		off := fs.Int("port-offset", -1, "port offset (default 100 × slot count)")
		clone := fs.String("clone", "", "clone this repo URL into PATH first")
		pos, err := parse(fs, args, 2)
		if err != nil {
			return err
		}
		var s *board.Slot
		if *clone != "" {
			s, err = a.b.AddSlotClone(pos[0], pos[1], *clone, *off)
		} else {
			s, err = a.b.AddSlot(pos[0], pos[1], *off)
		}
		if err != nil {
			return err
		}
		return a.out(s, func() {
			fmt.Fprintf(a.stdout, "slot %s %s (port offset %d)\n", s.Name, s.Path, s.PortOffset)
			fmt.Fprintf(a.stdout, "permissions: its agents may read and edit %s, and not the other slots (%s)\n", s.Path, board.PermissionsFile)
			fmt.Fprintf(a.stdout, "next: allow Claude to work there once with `crew slot trust %s`\n", s.Name)
		})
	case "permissions":
		// (re)write every slot's Claude permission rules, e.g. for slots added before crew did this
		if _, err := parse(flag.NewFlagSet("slot permissions", flag.ContinueOnError), args, 0); err != nil {
			return err
		}
		files, err := a.b.WriteSlotPermissions()
		if err != nil {
			return err
		}
		return a.out(files, func() {
			for _, f := range files {
				fmt.Fprintln(a.stdout, "wrote", f)
			}
		})
	case "trust":
		// Claude Code only starts background sessions in folders you've trusted. Open it there
		// so you can accept its prompt yourself; crew never accepts it for you.
		pos, err := parse(flag.NewFlagSet("slot trust", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		s, err := a.b.Slot(pos[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "Opening Claude in %s.\nIf it asks whether to trust this folder, accept, then type /exit.\n\n", s.Path)
		claude := os.Getenv("CREW_CLAUDE")
		if claude == "" {
			claude = "claude"
		}
		cmd := exec.Command(claude)
		cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = s.Path, os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("claude: %w", err)
		}
		fmt.Fprintf(a.stdout, "\nDone. Agents can now start in slot %s.\n", s.Name)
		return nil
	case "ls":
		if _, err := parse(flag.NewFlagSet("slot ls", flag.ContinueOnError), args, 0); err != nil {
			return err
		}
		ss, err := a.b.Slots()
		if err != nil {
			return err
		}
		return a.out(ss, func() {
			a.table("NAME\tPORTS\tAGENT\tPATH", func(w io.Writer) {
				for _, s := range ss {
					fmt.Fprintf(w, "%s\t+%d\t%s\t%s\n", s.Name, s.PortOffset, orDash(s.Agent), s.Path)
				}
			})
		})
	case "lease":
		fs := flag.NewFlagSet("slot lease", flag.ContinueOnError)
		agent := fs.String("agent", "", "agent id or name")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		if *agent == "" {
			return usageError{"slot lease: --agent required"}
		}
		ag, err := a.b.Agent(*agent)
		if err != nil {
			return err
		}
		s, err := a.b.LeaseSlot(pos[0], ag.ID)
		if err != nil {
			return err
		}
		return a.out(s, func() { fmt.Fprintf(a.stdout, "slot %s leased to %s\n", s.Name, s.Agent) })
	case "release":
		pos, err := parse(flag.NewFlagSet("slot release", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		s, err := a.b.ReleaseSlot(pos[0])
		if err != nil {
			return err
		}
		return a.out(s, func() { fmt.Fprintf(a.stdout, "slot %s released\n", s.Name) })
	}
	return usageError{"slot: add | ls | trust | permissions | lease | release"}
}

// ---- agents

func (a *app) printAgents(as []*board.Agent) {
	a.table("ID\tNAME\tSTATUS\tSTAGE\tBRANCH\tSLOT\tMSGS\tDECISIONS\tLAST", func(w io.Writer) {
		for _, x := range as {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n", x.ID, x.Name, x.Status, orDash(x.Stage), orDash(x.Branch), orDash(x.Slot), x.Pending, x.Decisions, short(orDash(x.LastMessage), 50))
		}
	})
}

func (a *app) printAgent(x *board.Agent) {
	fmt.Fprintf(a.stdout, "%s (#%d, %s)\nstatus   %s%s\nsession  %s  bg %s\nconversation %s\nslot     %s %s (ports +%d)\nitem     %s %s %s\npending  %d\ndecisions %d open\n\n%s\n",
		x.Name, x.ID, x.Role, x.Status, map[bool]string{true: " (attached)"}[x.Attached], x.SessionID, orDash(x.BgID),
		orDash(x.Conversation), orDash(x.Slot), x.Dir, x.PortOffset, ptrStr(x.ItemID), x.Stage, x.Branch, x.Pending, x.Decisions, x.LastMessage)
}

func ptrStr(p *int64) string {
	if p == nil {
		return "-"
	}
	return strconv.FormatInt(*p, 10)
}

func (a *app) agent(sub string, args []string) error {
	switch sub {
	case "start":
		fs := flag.NewFlagSet("agent start", flag.ContinueOnError)
		slot := fs.String("slot", "", "slot name (default: first free)")
		role := fs.String("role", "worker", "worker|techlead|intern|reviewer|director")
		model := fs.String("model", "", "claude --model")
		name := fs.String("name", "", "agent name (default: generated)")
		prompt := fs.String("prompt", "", "first prompt (- for stdin)")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "item id")
		if err != nil {
			return err
		}
		if *prompt == "-" {
			if *prompt, err = a.textArg([]string{"-"}); err != nil {
				return err
			}
		}
		ag, err := a.b.StartAgent(board.StartOpts{ItemID: id, Slot: *slot, Role: *role, Model: *model, Name: *name, Prompt: *prompt})
		if err != nil {
			return err
		}
		return a.out(ag, func() { fmt.Fprintf(a.stdout, "started %s in %s on %s\n", ag.Name, ag.Dir, ag.Branch) })
	case "ls":
		if _, err := parse(flag.NewFlagSet("agent ls", flag.ContinueOnError), args, 0); err != nil {
			return err
		}
		as, err := a.b.Agents()
		if err != nil {
			return err
		}
		return a.out(as, func() { a.printAgents(as) })
	case "show":
		fs := flag.NewFlagSet("agent show", flag.ContinueOnError)
		sid := fs.String("session", "", "Claude session id")
		branch := fs.String("branch", "", "branch name")
		repo := fs.String("repo", "", "repo name (with --branch)")
		pos, err := parse(fs, args, 0)
		if err != nil {
			return err
		}
		var ag *board.Agent
		switch {
		case *sid != "":
			ag, err = a.b.AgentBySession(*sid)
		case *branch != "":
			ag, err = a.b.AgentByBranch(*branch, *repo)
		case len(pos) == 1:
			ag, err = a.b.Agent(pos[0])
		default:
			return usageError{"agent show: ID|NAME, --session SID or --branch B"}
		}
		if err != nil {
			return err
		}
		ag.Conversation, _ = a.b.ConversationID(ag)
		return a.out(ag, func() { a.printAgent(ag) })
	case "stop":
		pos, err := parse(flag.NewFlagSet("agent stop", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		ag, err := a.b.StopAgent(pos[0])
		if err != nil {
			return err
		}
		return a.out(ag, func() { fmt.Fprintf(a.stdout, "stopped %s\n", ag.Name) })
	case "attach-mark":
		pos, err := parse(flag.NewFlagSet("agent attach-mark", flag.ContinueOnError), args, 2)
		if err != nil {
			return err
		}
		on := pos[1] == "on"
		if !on && pos[1] != "off" {
			return usageError{"agent attach-mark ID on|off"}
		}
		ag, err := a.b.SetAttached(pos[0], on)
		if err != nil {
			return err
		}
		return a.out(ag, func() { fmt.Fprintf(a.stdout, "%s attached=%v\n", ag.Name, ag.Attached) })
	case "background", "bg":
		fs := flag.NewFlagSet("agent background", flag.ContinueOnError)
		cont := fs.Bool("continue", false, "it was interrupted mid-turn: tell it to continue where it left off")
		prompt := fs.String("prompt", "", "something to tell it as it goes back")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		ag, err := a.b.Background(pos[0], *cont, *prompt)
		if err != nil {
			return err
		}
		return a.out(ag, func() {
			fmt.Fprintf(a.stdout, "%s is running in the background (%s) · watch it: claude attach %s\n", ag.Name, ag.Status, ag.BgID)
		})
	case "revive":
		fs := flag.NewFlagSet("agent revive", flag.ContinueOnError)
		prompt := fs.String("prompt", "", "something to tell it as it comes back")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		ag, err := a.b.Revive(pos[0], *prompt)
		if err != nil {
			return err
		}
		return a.out(ag, func() {
			fmt.Fprintf(a.stdout, "revived %s (%s) · watch it: claude attach %s\n", ag.Name, ag.Status, ag.BgID)
		})
	case "rm":
		fs := flag.NewFlagSet("agent rm", flag.ContinueOnError)
		force := fs.Bool("force", false, "also remove running agents (stops them first)")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		var removed []*board.Agent
		if pos[0] == "all" {
			removed, err = a.b.RemoveAll(*force)
		} else {
			var ag *board.Agent
			if ag, err = a.b.RemoveAgent(pos[0], *force); ag != nil {
				removed = append(removed, ag)
			}
		}
		if err != nil {
			return err
		}
		return a.out(removed, func() {
			if len(removed) == 0 {
				fmt.Fprintln(a.stdout, "nothing to remove (only stopped agents; --force for running ones)")
			}
			for _, ag := range removed {
				fmt.Fprintf(a.stdout, "removed %s\n", ag.Name)
			}
		})
	}
	return usageError{"agent: start | ls | show | stop | background | revive | rm | attach-mark"}
}

// ---- messages

func (a *app) defaultFrom() string {
	if id := os.Getenv("CREW_AGENT_ID"); id != "" {
		if ag, err := a.b.Agent(id); err == nil {
			return ag.Name
		}
	}
	return "you"
}

func (a *app) msg(sub string, args []string) error {
	switch sub {
	case "post":
		fs := flag.NewFlagSet("msg post", flag.ContinueOnError)
		to := fs.String("to", "", "agent id or name")
		item := fs.Int64("item", 0, "item id (routes to its agent)")
		kind := fs.String("kind", "note", "note|work|review")
		from := fs.String("from", "", "sender (default: $CREW_AGENT_ID's name or 'you')")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		text, err := a.textArg(pos)
		if err != nil {
			return err
		}
		if *from == "" {
			*from = a.defaultFrom()
		}
		m, err := a.b.PostMessage(board.PostOpts{To: *to, ItemID: *item, Kind: *kind, From: *from, Text: text})
		if err != nil {
			return err
		}
		return a.out(m, func() {
			fmt.Fprintf(a.stdout, "message %d to %s: %s\n", m.ID, orDash(m.To), m.Status)
			if m.ToAgentID != nil && m.Status == "pending" {
				if ag, err := a.b.AgentByID(*m.ToAgentID); err == nil && ag.Status == board.StatusStale {
					fmt.Fprintf(a.stdout, "%s is stale, so it waits until you revive it: crew agent revive %d\n", ag.Name, ag.ID)
				}
			}
		})
	case "ls":
		fs := flag.NewFlagSet("msg ls", flag.ContinueOnError)
		to := fs.String("to", "", "agent id or name")
		status := fs.String("status", "", "pending|delivered|read")
		if _, err := parse(fs, args, 0); err != nil {
			return err
		}
		ms, err := a.b.Messages(*to, *status)
		if err != nil {
			return err
		}
		return a.out(ms, func() {
			a.table("ID\tSTATUS\tFROM\tTO\tITEM\tKIND\tTEXT", func(w io.Writer) {
				for _, m := range ms {
					fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", m.ID, m.Status, m.From, orDash(m.To), ptrStr(m.ItemID), m.Kind, short(m.Text, 60))
				}
			})
		})
	case "ack":
		pos, err := parse(flag.NewFlagSet("msg ack", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "message id")
		if err != nil {
			return err
		}
		m, err := a.b.AckMessage(id)
		if err != nil {
			return err
		}
		return a.out(m, func() { fmt.Fprintf(a.stdout, "message %d %s\n", m.ID, m.Status) })
	}
	return usageError{"msg: post | ls | ack"}
}

// ---- comments

func (a *app) comment(sub string, args []string) error {
	switch sub {
	case "add":
		fs := flag.NewFlagSet("comment add", flag.ContinueOnError)
		var c board.Comment
		item := fs.Int64("item", 0, "item id")
		doc := fs.Int64("document", 0, "document id")
		parent := fs.Int64("parent", 0, "parent comment id")
		fs.StringVar(&c.Repo, "repo", "", "repo")
		fs.StringVar(&c.Branch, "branch", "", "branch")
		fs.StringVar(&c.Commit, "commit", "", "commit")
		fs.StringVar(&c.File, "file", "", "file path (repo-relative)")
		fs.IntVar(&c.StartLine, "start", 0, "start line")
		fs.IntVar(&c.EndLine, "end", 0, "end line")
		excerpt := fs.String("excerpt-file", "", "file with excerpt lines, or - for stdin")
		fs.StringVar(&c.Text, "text", "", "comment text")
		fs.StringVar(&c.Author, "author", "", "author")
		stdinJSON := fs.Bool("stdin-json", false, "read a JSON array of comments on stdin")
		send := fs.Bool("send", false, "also send them as one review message to the branch's agent")
		if _, err := parse(fs, args, 0); err != nil {
			return err
		}
		var cs []board.Comment
		if *stdinJSON {
			raw, err := io.ReadAll(a.stdin)
			if err != nil {
				return err
			}
			raw = []byte(strings.TrimSpace(string(raw)))
			if len(raw) > 0 && raw[0] == '{' {
				raw = append(append([]byte("["), raw...), ']')
			}
			if err := json.Unmarshal(raw, &cs); err != nil {
				return fmt.Errorf("comment add --stdin-json: %w", err)
			}
		} else {
			if *item != 0 {
				c.ItemID = item
			}
			if *doc != 0 {
				c.DocumentID = doc
			}
			if *parent != 0 {
				c.ParentID = parent
			}
			if *excerpt != "" {
				var raw []byte
				var err error
				if *excerpt == "-" {
					raw, err = io.ReadAll(a.stdin)
				} else {
					raw, err = os.ReadFile(*excerpt)
				}
				if err != nil {
					return err
				}
				c.Excerpt = strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			}
			cs = []board.Comment{c}
		}
		if len(cs) == 0 {
			return errors.New("no comments given")
		}
		added, err := a.b.AddComments(cs)
		if err != nil {
			return err
		}
		res := struct {
			Comments []*board.Comment `json:"comments"`
			Message  *board.Message   `json:"message,omitempty"`
		}{Comments: added}
		if *send {
			if res.Message, err = a.b.SendComments(added, ""); err != nil {
				return fmt.Errorf("comments saved but not sent: %w", err)
			}
			for i, c := range added {
				added[i], _ = a.b.Comment(c.ID)
			}
		}
		return a.out(res, func() {
			fmt.Fprintf(a.stdout, "added %d comment(s)", len(added))
			if res.Message != nil {
				fmt.Fprintf(a.stdout, ", sent to %s as message %d (%s)", res.Message.To, res.Message.ID, res.Message.Status)
			}
			fmt.Fprintln(a.stdout)
		})
	case "ls":
		fs := flag.NewFlagSet("comment ls", flag.ContinueOnError)
		var f board.CommentFilter
		fs.Int64Var(&f.ItemID, "item", 0, "item id")
		fs.Int64Var(&f.DocumentID, "document", 0, "document id")
		fs.StringVar(&f.Branch, "branch", "", "branch")
		fs.StringVar(&f.Repo, "repo", "", "repo")
		fs.StringVar(&f.Status, "status", "", "open|sent|resolved")
		if _, err := parse(fs, args, 0); err != nil {
			return err
		}
		cs, err := a.b.Comments(f)
		if err != nil {
			return err
		}
		return a.out(cs, func() {
			a.table("ID\tSTATUS\tWHERE\tTEXT", func(w io.Writer) {
				for _, c := range cs {
					fmt.Fprintf(w, "%d\t%s\t%s:%d\t%s\n", c.ID, c.Status, c.File, c.StartLine, short(c.Text, 60))
				}
			})
		})
	case "resolve":
		pos, err := parse(flag.NewFlagSet("comment resolve", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "comment id")
		if err != nil {
			return err
		}
		c, err := a.b.SetCommentStatus(id, "resolved")
		if err != nil {
			return err
		}
		return a.out(c, func() { fmt.Fprintf(a.stdout, "comment %d resolved\n", c.ID) })
	}
	return usageError{"comment: add | ls | resolve"}
}

// ---- documents

func (a *app) doc(sub string, args []string) error {
	switch sub {
	case "add":
		fs := flag.NewFlagSet("doc add", flag.ContinueOnError)
		kind := fs.String("kind", "plan", "plan|plan-review|code-review|note")
		pos, err := parse(fs, args, 2)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "item id")
		if err != nil {
			return err
		}
		var content []byte
		if pos[1] == "-" {
			content, err = io.ReadAll(a.stdin)
		} else {
			content, err = os.ReadFile(pos[1])
		}
		if err != nil {
			return err
		}
		d, err := a.b.AddDocument(id, *kind, content)
		if err != nil {
			return err
		}
		return a.out(d, func() { fmt.Fprintf(a.stdout, "document %d %s v%d %s\n", d.ID, d.Kind, d.Version, d.Path) })
	case "ls":
		pos, err := parse(flag.NewFlagSet("doc ls", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "item id")
		if err != nil {
			return err
		}
		ds, err := a.b.Documents(id)
		if err != nil {
			return err
		}
		return a.out(ds, func() {
			a.table("ID\tKIND\tVER\tPATH", func(w io.Writer) {
				for _, d := range ds {
					fmt.Fprintf(w, "%d\t%s\t%d\t%s\n", d.ID, d.Kind, d.Version, d.Path)
				}
			})
		})
	case "show":
		pos, err := parse(flag.NewFlagSet("doc show", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "document id")
		if err != nil {
			return err
		}
		d, err := a.b.Document(id, true)
		if err != nil {
			return err
		}
		return a.out(d, func() { fmt.Fprint(a.stdout, d.Content) })
	}
	return usageError{"doc: add | ls | show"}
}

// ---- events

func (a *app) events(sub string, args []string) error {
	if sub != "ls" {
		return usageError{"event: --hook | ls [--since N]"}
	}
	fs := flag.NewFlagSet("event ls", flag.ContinueOnError)
	since := fs.Int64("since", 0, "only events after this id")
	limit := fs.Int("limit", 200, "max events")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	evs, err := a.b.Events(*since, *limit)
	if err != nil {
		return err
	}
	return a.out(evs, func() {
		for _, e := range evs {
			fmt.Fprintf(a.stdout, "%d %s %-18s agent=%s item=%s %s\n", e.ID, e.TS, e.Type, ptrStr(e.AgentID), ptrStr(e.ItemID), e.Data)
		}
	})
}
