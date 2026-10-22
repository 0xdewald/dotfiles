// Package mcpserver exposes board operations as MCP tools.
package mcpserver

import (
	"context"
	"errors"
	"os"
	"strconv"

	"github.com/0xdewald/dotfiles/crew/internal/board"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// New builds the MCP server. callerID returns the calling agent's id
// ($CREW_AGENT_ID by default); "" means not an agent.
func New(b *board.Board, callerID func() string) *mcp.Server {
	if callerID == nil {
		callerID = func() string { return os.Getenv("CREW_AGENT_ID") }
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "crew", Version: "0.1.1"}, nil)
	caller := func() (*board.Agent, error) {
		id := callerID()
		if id == "" {
			return nil, errors.New("CREW_AGENT_ID is not set: this tool is for crew agents")
		}
		return b.Agent(id)
	}

	mcp.AddTool(s, &mcp.Tool{Name: "board_status", Description: "Work item counts by stage, plus all agents and slots."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, *board.Status, error) {
			st, err := b.Status()
			return nil, st, err
		})

	type listItemsIn struct {
		Stage string `json:"stage,omitempty" jsonschema:"only items in this stage"`
	}
	type itemsOut struct {
		Items []*board.Item `json:"items"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "list_items", Description: "List work items, optionally by stage."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listItemsIn) (*mcp.CallToolResult, itemsOut, error) {
			items, err := b.Items(in.Stage)
			return nil, itemsOut{items}, err
		})

	type idIn struct {
		ID int64 `json:"id" jsonschema:"work item id"`
	}
	type itemOut struct {
		Item      *board.Item       `json:"item"`
		Ticket    *board.Ticket     `json:"ticket,omitempty"`
		Documents []*board.Document `json:"documents"`
		Comments  []*board.Comment  `json:"comments"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "show_item", Description: "Show a work item with its ticket, documents and comments."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, itemOut, error) {
			var out itemOut
			var err error
			if out.Item, err = b.Item(in.ID); err != nil {
				return nil, out, err
			}
			if out.Item.TicketID != nil {
				out.Ticket, _ = b.Ticket(strconv.FormatInt(*out.Item.TicketID, 10))
			}
			if out.Documents, err = b.Documents(in.ID); err != nil {
				return nil, out, err
			}
			out.Comments, err = b.Comments(board.CommentFilter{ItemID: in.ID})
			return nil, out, err
		})

	type moveIn struct {
		ID    int64  `json:"id" jsonschema:"work item id"`
		Stage string `json:"stage" jsonschema:"target stage"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "move_item", Description: "Move a work item to another stage (lifecycle rules apply)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in moveIn) (*mcp.CallToolResult, *board.Item, error) {
			it, err := b.MoveItem(in.ID, in.Stage, false)
			return nil, it, err
		})

	type postIn struct {
		To     string `json:"to,omitempty" jsonschema:"recipient agent id or name"`
		ItemID int64  `json:"item_id,omitempty" jsonschema:"work item id; routes to its agent"`
		Kind   string `json:"kind,omitempty" jsonschema:"note, work or review (default note)"`
		Text   string `json:"text"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "post_message", Description: "Post a message to an agent or work item; idle agents get it right away."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in postIn) (*mcp.CallToolResult, *board.Message, error) {
			from := "director"
			if a, err := caller(); err == nil {
				from = a.Name
			}
			m, err := b.PostMessage(board.PostOpts{To: in.To, ItemID: in.ItemID, Kind: in.Kind, From: from, Text: in.Text})
			return nil, m, err
		})

	type listMsgIn struct {
		To     string `json:"to,omitempty" jsonschema:"agent id or name"`
		Status string `json:"status,omitempty" jsonschema:"pending, delivered or read"`
	}
	type msgsOut struct {
		Messages []*board.Message `json:"messages"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "list_messages", Description: "List board messages."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listMsgIn) (*mcp.CallToolResult, msgsOut, error) {
			ms, err := b.Messages(in.To, in.Status)
			return nil, msgsOut{ms}, err
		})

	type commentsOut struct {
		Comments []*board.Comment `json:"comments"`
	}
	type commentIn struct {
		ItemID     int64    `json:"item_id,omitempty"`
		DocumentID int64    `json:"document_id,omitempty"`
		ParentID   int64    `json:"parent_id,omitempty" jsonschema:"reply to this comment"`
		Repo       string   `json:"repo,omitempty"`
		Branch     string   `json:"branch,omitempty"`
		Commit     string   `json:"commit,omitempty"`
		File       string   `json:"file,omitempty" jsonschema:"repo-relative path"`
		StartLine  int      `json:"start_line,omitempty"`
		EndLine    int      `json:"end_line,omitempty"`
		Excerpt    []string `json:"excerpt,omitempty" jsonschema:"the commented lines, verbatim"`
		Text       string   `json:"text"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "add_comment", Description: "Add a line comment on code (repo, branch, file, lines) or on a document."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in commentIn) (*mcp.CallToolResult, commentsOut, error) {
			c := board.Comment{Repo: in.Repo, Branch: in.Branch, Commit: in.Commit, File: in.File,
				StartLine: in.StartLine, EndLine: in.EndLine, Excerpt: in.Excerpt, Text: in.Text}
			for _, p := range []struct {
				v   int64
				dst **int64
			}{{in.ItemID, &c.ItemID}, {in.DocumentID, &c.DocumentID}, {in.ParentID, &c.ParentID}} {
				if p.v != 0 {
					*p.dst = &p.v
				}
			}
			if a, err := caller(); err == nil {
				c.Author = a.Name
			}
			cs, err := b.AddComments([]board.Comment{c})
			return nil, commentsOut{cs}, err
		})

	type listCommentsIn struct {
		ItemID int64  `json:"item_id,omitempty"`
		Branch string `json:"branch,omitempty"`
		Status string `json:"status,omitempty" jsonschema:"open, sent or resolved"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "list_comments", Description: "List comments by item, branch or status."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listCommentsIn) (*mcp.CallToolResult, commentsOut, error) {
			cs, err := b.Comments(board.CommentFilter{ItemID: in.ItemID, Branch: in.Branch, Status: in.Status})
			return nil, commentsOut{cs}, err
		})

	type doneIn struct {
		Summary string `json:"summary" jsonschema:"what you did, how it was tested, anything left open"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "report_done", Description: "Report your work item done. Push your branch first: the item moves coding → pushed only if the branch is on origin."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in doneIn) (*mcp.CallToolResult, *board.Item, error) {
			a, err := caller()
			if err != nil {
				return nil, nil, err
			}
			it, err := b.ReportDone(a.ID, in.Summary)
			return nil, it, err
		})
	type askIn struct {
		Question       string   `json:"question" jsonschema:"one question, answerable on its own"`
		Options        []string `json:"options,omitempty" jsonschema:"the choices you see"`
		Recommendation string   `json:"recommendation,omitempty" jsonschema:"the option you would pick"`
		Reason         string   `json:"reason,omitempty" jsonschema:"why you recommend it"`
	}
	type askOut struct {
		ID   int64  `json:"id"`
		Next string `json:"next"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "ask_decision", Description: "Put a question for a human on the crew board instead of asking in chat. " +
		"Call it once per question, with the options, your recommendation and why, then end your turn: " +
		"the answers to all your open questions arrive together as one message."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in askIn) (*mcp.CallToolResult, askOut, error) {
			a, err := caller()
			if err != nil {
				return nil, askOut{}, err
			}
			d, err := b.AskDecision(a.ID, in.Question, in.Options, in.Recommendation, in.Reason)
			if err != nil {
				return nil, askOut{}, err
			}
			out := askOut{ID: d.ID, Next: "Decision " + strconv.FormatInt(d.ID, 10) + " is on the board. " +
				"Ask any other questions the same way, then end your turn now; the answer arrives as a message."}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out.Next}}}, out, nil
		})

	type listDecisionsIn struct {
		Agent  string `json:"agent,omitempty" jsonschema:"agent id or name"`
		Status string `json:"status,omitempty" jsonschema:"open (default), answered, cancelled or all"`
	}
	type decisionsOut struct {
		Decisions []*board.Decision `json:"decisions"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "list_decisions", Description: "List agents' decisions (open ones by default)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listDecisionsIn) (*mcp.CallToolResult, decisionsOut, error) {
			ds, err := b.Decisions(board.DecisionFilter{Agent: in.Agent, Status: in.Status})
			return nil, decisionsOut{ds}, err
		})

	type answerIn struct {
		ID     int64  `json:"id" jsonschema:"decision id"`
		Answer string `json:"answer,omitempty" jsonschema:"the answer; leave out with accept"`
		Accept bool   `json:"accept,omitempty" jsonschema:"answer with the agent's recommendation"`
	}
	type answerOut struct {
		Decision *board.Decision `json:"decision"`
		Message  *board.Message  `json:"message,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "answer_decision", Description: "Answer an agent's decision. Once all its open decisions are answered, " +
		"the agent gets every answer as one message."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in answerIn) (*mcp.CallToolResult, answerOut, error) {
			by := "director"
			if a, err := caller(); err == nil {
				by = a.Name
			}
			var out answerOut
			var err error
			if in.Accept {
				out.Decision, out.Message, err = b.AcceptDecision(in.ID, by)
			} else {
				out.Decision, out.Message, err = b.AnswerDecision(in.ID, in.Answer, by)
			}
			return nil, out, err
		})
	return s
}

// Run serves on stdio until the client disconnects.
func Run(ctx context.Context, b *board.Board) error {
	return New(b, nil).Run(ctx, &mcp.StdioTransport{})
}
