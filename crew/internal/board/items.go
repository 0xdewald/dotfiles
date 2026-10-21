package board

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Ticket is a Jira key and/or pasted text.
type Ticket struct {
	ID      int64           `json:"id"`
	JiraKey string          `json:"jira_key"`
	Title   string          `json:"title"`
	Text    string          `json:"text"`
	Fetched json.RawMessage `json:"fetched,omitempty"`
	Created string          `json:"created"`
}

// AddTicket stores a ticket. fetched may be nil.
func (b *Board) AddTicket(key, title, text string, fetched []byte) (*Ticket, error) {
	var fj any
	if len(fetched) > 0 {
		fj = string(fetched)
	}
	res, err := b.DB.Exec(`INSERT INTO tickets(jira_key, title, text, fetched_json, created) VALUES(?,?,?,?,?)`,
		nullStr(key), title, text, fj, b.now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	b.Event("ticket.add", nil, nil, map[string]any{"ticket_id": id, "jira_key": key})
	return b.Ticket(strconv.FormatInt(id, 10))
}

const ticketCols = `id, COALESCE(jira_key,''), title, text, COALESCE(fetched_json,''), created`

func scanTicket(s interface{ Scan(...any) error }) (*Ticket, error) {
	var t Ticket
	var fj string
	if err := s.Scan(&t.ID, &t.JiraKey, &t.Title, &t.Text, &fj, &t.Created); err != nil {
		return nil, err
	}
	if fj != "" {
		t.Fetched = json.RawMessage(fj)
	}
	return &t, nil
}

// Ticket looks up by numeric id or Jira key (newest wins).
func (b *Board) Ticket(ref string) (*Ticket, error) {
	q := `SELECT ` + ticketCols + ` FROM tickets WHERE jira_key = ? ORDER BY id DESC LIMIT 1`
	var arg any = ref
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		q, arg = `SELECT `+ticketCols+` FROM tickets WHERE id = ?`, id
	}
	t, err := scanTicket(b.DB.QueryRow(q, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("ticket %s: %w", ref, ErrNotFound)
	}
	return t, err
}

func (b *Board) Tickets() ([]*Ticket, error) {
	rows, err := b.DB.Query(`SELECT ` + ticketCols + ` FROM tickets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		t.Fetched = nil // keep listings small
		out = append(out, t)
	}
	return out, rows.Err()
}

// Stages, in lifecycle order.
var Stages = []string{"new", "researching", "planned", "plan-review", "awaiting-you", "queued",
	"coding", "pushed", "reviewing", "pr-draft", "human-review", "merged", "closed", "escalated"}

// transitions per plan §4; any stage may also go to closed or escalated.
var transitions = map[string][]string{
	"new":          {"researching", "queued"}, // queued: milestone 1 has no tech lead yet
	"researching":  {"planned"},
	"planned":      {"plan-review", "awaiting-you"},
	"plan-review":  {"awaiting-you", "planned"},
	"awaiting-you": {"queued", "researching"},
	"queued":       {"coding"},
	"coding":       {"pushed"},
	"pushed":       {"reviewing", "coding"},
	"reviewing":    {"coding", "pr-draft"},
	"pr-draft":     {"human-review"},
	"human-review": {"coding", "merged"},
	"merged":       {},
	"closed":       {},
	"escalated":    {"queued", "coding"},
}

func validStage(s string) bool { _, ok := transitions[s]; return ok }

// CanMove reports whether from → to is allowed without force.
func CanMove(from, to string) bool {
	if to == "closed" || to == "escalated" {
		return from != to
	}
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Item is a unit of work on one branch.
type Item struct {
	ID       int64  `json:"id"`
	TicketID *int64 `json:"ticket_id"`
	JiraKey  string `json:"jira_key"`
	Title    string `json:"title"`
	Stage    string `json:"stage"`
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	AgentID  *int64 `json:"agent_id"`
	Agent    string `json:"agent"` // agent name
	Rounds   int    `json:"rounds"`
	Created  string `json:"created"`
	Updated  string `json:"updated"`
}

const itemSel = `SELECT w.id, w.ticket_id, COALESCE(t.jira_key,''), w.title, w.stage, w.repo, w.branch, w.agent_id,
	COALESCE(a.name,''), w.rounds, w.created, w.updated
	FROM work_items w LEFT JOIN tickets t ON t.id = w.ticket_id LEFT JOIN agents a ON a.id = w.agent_id`

func scanItem(s interface{ Scan(...any) error }) (*Item, error) {
	var i Item
	err := s.Scan(&i.ID, &i.TicketID, &i.JiraKey, &i.Title, &i.Stage, &i.Repo, &i.Branch, &i.AgentID, &i.Agent, &i.Rounds, &i.Created, &i.Updated)
	return &i, err
}

// NewItem creates a work item for a ticket. Branch defaults to agent/<key|id>.
func (b *Board) NewItem(ticketRef, title, repo, branch string) (*Item, error) {
	var tid any
	var t *Ticket
	if ticketRef != "" {
		var err error
		if t, err = b.Ticket(ticketRef); err != nil {
			return nil, err
		}
		tid = t.ID
		if title == "" {
			title = t.Title
			if title == "" {
				title = t.JiraKey
			}
			if title == "" {
				title = firstLine(t.Text, 80)
			}
		}
	}
	now := b.now()
	tx, err := b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO work_items(ticket_id, title, stage, repo, branch, created, updated) VALUES(?,?,?,?,?,?,?)`,
		tid, title, "new", repo, branch, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if branch == "" {
		suffix := strconv.FormatInt(id, 10)
		if t != nil && t.JiraKey != "" {
			suffix = t.JiraKey
		}
		branch = "agent/" + suffix
		if _, err := tx.Exec(`UPDATE work_items SET branch = ? WHERE id = ?`, branch, id); err != nil {
			return nil, err
		}
	}
	if err := b.event(tx, "item.new", nil, &id, map[string]any{"branch": branch, "repo": repo}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.Item(id)
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func (b *Board) Item(id int64) (*Item, error) {
	i, err := scanItem(b.DB.QueryRow(itemSel+` WHERE w.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("item %d: %w", id, ErrNotFound)
	}
	return i, err
}

// Items lists items, optionally filtered by stage.
func (b *Board) Items(stage string) ([]*Item, error) {
	q, args := itemSel+` ORDER BY w.id`, []any{}
	if stage != "" {
		q, args = itemSel+` WHERE w.stage = ? ORDER BY w.id`, []any{stage}
	}
	rows, err := b.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Item{}
	for rows.Next() {
		i, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// MoveItem changes an item's stage, enforcing the lifecycle unless force.
// reviewing → coding counts as a fix round.
func (b *Board) MoveItem(id int64, to string, force bool) (*Item, error) {
	if !validStage(to) {
		return nil, fmt.Errorf("unknown stage %q (stages: %s)", to, strings.Join(Stages, ", "))
	}
	tx, err := b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := b.moveTx(tx, id, to, force); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.Item(id)
}

func (b *Board) moveTx(tx *sql.Tx, id int64, to string, force bool) error {
	var from string
	if err := tx.QueryRow(`SELECT stage FROM work_items WHERE id = ?`, id).Scan(&from); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("item %d: %w", id, ErrNotFound)
		}
		return err
	}
	if from == to {
		return nil
	}
	if !force && !CanMove(from, to) {
		return fmt.Errorf("item %d: can't move %s → %s (allowed: %s; use --force)", id, from, to, strings.Join(append(transitions[from], "closed", "escalated"), ", "))
	}
	round := 0
	if from == "reviewing" && to == "coding" {
		round = 1
	}
	if _, err := tx.Exec(`UPDATE work_items SET stage = ?, rounds = rounds + ?, updated = ? WHERE id = ?`, to, round, b.now(), id); err != nil {
		return err
	}
	return b.event(tx, "item.move", nil, &id, map[string]any{"from": from, "to": to, "force": force && !CanMove(from, to)})
}
