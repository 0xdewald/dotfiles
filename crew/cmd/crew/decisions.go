package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/0xdewald/dotfiles/crew/internal/board"
)

// answered is what answer and cancel print: the decision, and the message that took the answers
// to the agent once its last open decision was settled (null until then).
type answered struct {
	Decision *board.Decision `json:"decision"`
	Message  *board.Message  `json:"message"`
}

func (a *app) decision(sub string, args []string) error {
	switch sub {
	case "ls":
		fs := flag.NewFlagSet("decision ls", flag.ContinueOnError)
		agent := fs.String("agent", "", "agent id or name")
		status := fs.String("status", "open", "open|answered|cancelled|all")
		if _, err := parse(fs, args, 0); err != nil {
			return err
		}
		ds, err := a.b.Decisions(board.DecisionFilter{Agent: *agent, Status: *status})
		if err != nil {
			return err
		}
		return a.out(ds, func() {
			a.table("ID\tSTATUS\tAGENT\tITEM\tQUESTION\tRECOMMENDED\tANSWER", func(w io.Writer) {
				for _, d := range ds {
					fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", d.ID, d.Status, orDash(d.Agent), ptrStr(d.ItemID),
						short(d.Question, 60), short(orDash(d.Recommendation), 30), short(orDash(d.Answer), 30))
				}
			})
		})
	case "show":
		pos, err := parse(flag.NewFlagSet("decision show", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "decision id")
		if err != nil {
			return err
		}
		d, err := a.b.Decision(id)
		if err != nil {
			return err
		}
		return a.out(d, func() { a.printDecision(d) })
	case "answer":
		fs := flag.NewFlagSet("decision answer", flag.ContinueOnError)
		accept := fs.Bool("accept", false, "answer with the agent's recommendation")
		by := fs.String("by", "", "who answers (default: $CREW_AGENT_ID's name or 'you')")
		pos, err := parse(fs, args, 1)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "decision id")
		if err != nil {
			return err
		}
		if *by == "" {
			*by = a.defaultFrom()
		}
		var r answered
		if *accept {
			if len(pos) > 1 {
				return usageError{"decision answer ID TEXT or ID --accept, not both"}
			}
			r.Decision, r.Message, err = a.b.AcceptDecision(id, *by)
		} else {
			if len(pos) < 2 {
				return usageError{"decision answer ID TEXT|- (or --accept)"}
			}
			text, terr := a.textArg(pos[1:])
			if terr != nil {
				return terr
			}
			r.Decision, r.Message, err = a.b.AnswerDecision(id, text, *by)
		}
		if err != nil {
			return err
		}
		return a.out(r, func() { a.printSettled(r, "answered") })
	case "cancel":
		pos, err := parse(flag.NewFlagSet("decision cancel", flag.ContinueOnError), args, 1)
		if err != nil {
			return err
		}
		id, err := atoi(pos[0], "decision id")
		if err != nil {
			return err
		}
		var r answered
		if r.Decision, r.Message, err = a.b.CancelDecision(id); err != nil {
			return err
		}
		return a.out(r, func() { a.printSettled(r, "cancelled") })
	}
	return usageError{"decision: ls | show | answer | cancel"}
}

func (a *app) printDecision(d *board.Decision) {
	fmt.Fprintf(a.stdout, "decision %d (%s) from %s, item %s\n\n%s\n", d.ID, d.Status, orDash(d.Agent), ptrStr(d.ItemID), d.Question)
	if len(d.Options) > 0 {
		fmt.Fprintln(a.stdout)
		for i, o := range d.Options {
			mark := ""
			if o == d.Recommendation {
				mark = "  (recommended)"
			}
			fmt.Fprintf(a.stdout, "  %d. %s%s\n", i+1, o, mark)
		}
	}
	if d.Recommendation != "" {
		fmt.Fprintf(a.stdout, "\nrecommends: %s\n", d.Recommendation)
	}
	if d.Reason != "" {
		fmt.Fprintf(a.stdout, "because:    %s\n", d.Reason)
	}
	if d.Status == "answered" {
		fmt.Fprintf(a.stdout, "\nanswer (%s): %s\n", d.AnsweredBy, d.Answer)
	}
}

func (a *app) printSettled(r answered, what string) {
	fmt.Fprintf(a.stdout, "decision %d %s\n", r.Decision.ID, what)
	if r.Message != nil {
		fmt.Fprintf(a.stdout, "answers sent to %s as message %d (%s)\n", r.Message.To, r.Message.ID, r.Message.Status)
		return
	}
	if ds, err := a.b.Decisions(board.DecisionFilter{Agent: fmt.Sprint(r.Decision.AgentID)}); err == nil && len(ds) > 0 {
		ids := make([]string, len(ds))
		for i, d := range ds {
			ids[i] = fmt.Sprint(d.ID)
		}
		fmt.Fprintf(a.stdout, "%s gets the answers once %s answered: %s\n", r.Decision.Agent,
			map[bool]string{true: "this one is", false: "these are"}[len(ds) == 1], strings.Join(ids, ", "))
	}
}
