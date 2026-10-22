package board

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// StatusAwaitingDecision: the agent ended its turn with decisions open on the board. It waits on a
// human, so it never goes stale; the answers resume it as one message.
const StatusAwaitingDecision = "awaiting-decision"

// Decision is a question an agent put on the board instead of asking in chat.
type Decision struct {
	ID             int64    `json:"id"`
	AgentID        int64    `json:"agent_id"`
	Agent          string   `json:"agent"` // agent name
	ItemID         *int64   `json:"item_id"`
	Question       string   `json:"question"`
	Options        []string `json:"options"`
	Recommendation string   `json:"recommendation"`
	Reason         string   `json:"reason"`
	Status         string   `json:"status"` // open, answered, cancelled
	Answer         string   `json:"answer"`
	AnsweredBy     string   `json:"answered_by"`
	MessageID      *int64   `json:"message_id"` // the message that took the answer to the agent
	Created        string   `json:"created"`
	Answered       string   `json:"answered"`
}

const decisionSel = `SELECT d.id, d.agent_id, COALESCE(a.name,''), d.item_id, d.question, d.options_json, d.recommendation,
	d.reason, d.status, d.answer, d.answered_by, d.message_id, d.created, COALESCE(d.answered,'')
	FROM decisions d LEFT JOIN agents a ON a.id = d.agent_id`

func scanDecision(s interface{ Scan(...any) error }) (*Decision, error) {
	var d Decision
	var opts string
	if err := s.Scan(&d.ID, &d.AgentID, &d.Agent, &d.ItemID, &d.Question, &opts, &d.Recommendation, &d.Reason,
		&d.Status, &d.Answer, &d.AnsweredBy, &d.MessageID, &d.Created, &d.Answered); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(opts), &d.Options)
	if d.Options == nil {
		d.Options = []string{}
	}
	return &d, nil
}

// AskDecision puts an agent's question on the board, with the options it sees, the one it
// recommends and why.
func (b *Board) AskDecision(agentID int64, question string, options []string, recommendation, reason string) (*Decision, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, errors.New("empty question")
	}
	a, err := b.AgentByID(agentID)
	if err != nil {
		return nil, err
	}
	var clean []string
	for _, o := range options {
		if o = strings.TrimSpace(o); o != "" {
			clean = append(clean, o)
		}
	}
	if clean == nil {
		clean = []string{}
	}
	oj, _ := json.Marshal(clean)
	res, err := b.DB.Exec(`INSERT INTO decisions(agent_id, item_id, question, options_json, recommendation, reason, created) VALUES(?,?,?,?,?,?,?)`,
		a.ID, a.ItemID, question, string(oj), strings.TrimSpace(recommendation), strings.TrimSpace(reason), b.now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	b.Event("decision.ask", &a.ID, a.ItemID, map[string]any{"decision_id": id, "question": truncate(question, 200)})
	// asked between turns (not mid-turn, as an agent does): it now waits on the answer
	if a.Status == StatusIdle || a.Status == StatusNeedsInput {
		b.setStatus(a.ID, StatusAwaitingDecision, nil)
	}
	return b.Decision(id)
}

func (b *Board) Decision(id int64) (*Decision, error) {
	d, err := scanDecision(b.DB.QueryRow(decisionSel+` WHERE d.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("decision %d: %w", id, ErrNotFound)
	}
	return d, err
}

// DecisionFilter narrows Decisions. Status "" means open; "all" means any.
type DecisionFilter struct {
	Agent  string // id or name
	Status string
}

func (b *Board) Decisions(f DecisionFilter) ([]*Decision, error) {
	q, args := decisionSel+` WHERE 1=1`, []any{}
	if f.Agent != "" {
		a, err := b.Agent(f.Agent)
		if err != nil {
			return nil, err
		}
		q += ` AND d.agent_id = ?`
		args = append(args, a.ID)
	}
	switch f.Status {
	case "":
		q += ` AND d.status = 'open'`
	case "all":
	case "open", "answered", "cancelled":
		q += ` AND d.status = ?`
		args = append(args, f.Status)
	default:
		return nil, fmt.Errorf("unknown decision status %q (open, answered, cancelled, all)", f.Status)
	}
	rows, err := b.DB.Query(q+` ORDER BY d.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Decision{}
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AnswerDecision records an answer. Answers are held until the agent's last open decision is
// answered (or cancelled), then go to it together as one message, delivered the usual way. The
// message is returned once it's sent (nil while other decisions are still open).
func (b *Board) AnswerDecision(id int64, answer, by string) (*Decision, *Message, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil, nil, errors.New("empty answer")
	}
	if by == "" {
		by = "you"
	}
	return b.closeDecision(id, "answered", answer, by)
}

// AcceptDecision answers a decision with the agent's own recommendation.
func (b *Board) AcceptDecision(id int64, by string) (*Decision, *Message, error) {
	d, err := b.Decision(id)
	if err != nil {
		return nil, nil, err
	}
	if d.Recommendation == "" {
		return nil, nil, fmt.Errorf("decision %d has no recommendation to accept", id)
	}
	return b.AnswerDecision(id, d.Recommendation, by)
}

// RejectDecision answers no to the agent's recommendation, with an optional reason.
func (b *Board) RejectDecision(id int64, reason, by string) (*Decision, *Message, error) {
	d, err := b.Decision(id)
	if err != nil {
		return nil, nil, err
	}
	if d.Recommendation == "" {
		return nil, nil, fmt.Errorf("decision %d has no recommendation to reject; answer it instead", id)
	}
	answer := "Rejected: " + d.Recommendation + "."
	if reason = strings.TrimSpace(reason); reason != "" {
		answer += " " + reason
	}
	return b.AnswerDecision(id, answer, by)
}

// CancelDecision withdraws a question nobody needs to answer. If it was the last open one, the
// answers already given go to the agent.
func (b *Board) CancelDecision(id int64) (*Decision, *Message, error) {
	return b.closeDecision(id, "cancelled", "", "")
}

func (b *Board) closeDecision(id int64, status, answer, by string) (*Decision, *Message, error) {
	tx, err := b.DB.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	var agentID int64
	var itemID *int64
	var cur string
	err = tx.QueryRow(`SELECT agent_id, item_id, status FROM decisions WHERE id = ?`, id).Scan(&agentID, &itemID, &cur)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("decision %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, nil, err
	}
	if cur != "open" {
		return nil, nil, fmt.Errorf("decision %d is already %s", id, cur)
	}
	if _, err := tx.Exec(`UPDATE decisions SET status = ?, answer = ?, answered_by = ?, answered = ? WHERE id = ?`,
		status, answer, by, b.now(), id); err != nil {
		return nil, nil, err
	}
	typ := "decision.answer"
	if status == "cancelled" {
		typ = "decision.cancel"
	}
	if err := b.event(tx, typ, &agentID, itemID, map[string]any{"decision_id": id, "by": by}); err != nil {
		return nil, nil, err
	}
	msgID, open, err := b.sendAnswersTx(tx, agentID, itemID, by)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	var m *Message
	if msgID != 0 {
		if _, err := b.deliver(agentID); err != nil {
			b.Event("msg.deliver_error", &agentID, itemID, map[string]any{"error": err.Error()})
		}
		m, _ = b.Message(msgID)
	} else if open == 0 {
		// every question withdrawn, nothing to tell it: it's simply idle again
		b.DB.Exec(`UPDATE agents SET status = ?, updated = ? WHERE id = ? AND status = ?`, StatusIdle, b.now(), agentID, StatusAwaitingDecision)
	}
	d, err := b.Decision(id)
	return d, m, err
}

// sendAnswersTx posts the agent's answered-but-unsent decisions as one message, once none are
// open. Returns the message id (0 if none was posted) and how many decisions are still open.
func (b *Board) sendAnswersTx(tx *sql.Tx, agentID int64, itemID *int64, from string) (int64, int, error) {
	var open int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM decisions WHERE agent_id = ? AND status = 'open'`, agentID).Scan(&open); err != nil {
		return 0, 0, err
	}
	if open > 0 {
		return 0, open, nil
	}
	rows, err := tx.Query(`SELECT id, question, answer, answered_by FROM decisions WHERE agent_id = ? AND status = 'answered' AND message_id IS NULL ORDER BY id`, agentID)
	if err != nil {
		return 0, 0, err
	}
	var ids []int64
	var sb strings.Builder
	sb.WriteString("Answers to your decisions:")
	for n := 1; rows.Next(); n++ {
		var id int64
		var q, ans, by string
		if err := rows.Scan(&id, &q, &ans, &by); err != nil {
			rows.Close()
			return 0, 0, err
		}
		ids = append(ids, id)
		fmt.Fprintf(&sb, "\n%d. %s\n   → %s", n, q, strings.ReplaceAll(ans, "\n", "\n     "))
		if from == "" {
			from = by // a cancel completing the batch: from whoever answered
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return 0, 0, nil
	}
	if from == "" {
		from = "you"
	}
	res, err := tx.Exec(`INSERT INTO messages("from", to_agent_id, item_id, kind, text, created) VALUES(?,?,?,'note',?,?)`,
		from, agentID, itemID, sb.String(), b.now())
	if err != nil {
		return 0, 0, err
	}
	msgID, _ := res.LastInsertId()
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE decisions SET message_id = ? WHERE id = ?`, msgID, id); err != nil {
			return 0, 0, err
		}
	}
	err = b.event(tx, "msg.post", &agentID, itemID, map[string]any{"message_id": msgID, "from": from, "kind": "note", "decisions": ids})
	return msgID, 0, err
}

// idleStatus is what an agent between turns shows: waiting on its decisions, or simply idle.
func idleStatus(a *Agent) string {
	if a.Decisions > 0 {
		return StatusAwaitingDecision
	}
	return StatusIdle
}

func decisionsLabel(n int) string { return strconv.Itoa(n) + " decision" + plural(n) }

func (b *Board) firstOpenQuestion(agentID int64) string {
	var q string
	b.DB.QueryRow(`SELECT question FROM decisions WHERE agent_id = ? AND status = 'open' ORDER BY id LIMIT 1`, agentID).Scan(&q)
	return truncate(oneLine(q), 120)
}
