package board

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Message is a board post addressed to an agent and/or item.
type Message struct {
	ID        int64  `json:"id"`
	From      string `json:"from"`
	ToAgentID *int64 `json:"to_agent_id"`
	To        string `json:"to"` // agent name
	ItemID    *int64 `json:"item_id"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	Created   string `json:"created"`
	Delivered string `json:"delivered"`
}

const msgSel = `SELECT m.id, m."from", m.to_agent_id, COALESCE(a.name,''), m.item_id, m.kind, m.text, m.status, m.created, COALESCE(m.delivered,'')
	FROM messages m LEFT JOIN agents a ON a.id = m.to_agent_id`

func scanMsg(s interface{ Scan(...any) error }) (*Message, error) {
	var m Message
	err := s.Scan(&m.ID, &m.From, &m.ToAgentID, &m.To, &m.ItemID, &m.Kind, &m.Text, &m.Status, &m.Created, &m.Delivered)
	return &m, err
}

var msgKinds = map[string]bool{"note": true, "work": true, "review": true}

// PostOpts addresses a message: To (agent ref) wins over ItemID.
type PostOpts struct {
	To     string
	ItemID int64
	Kind   string
	From   string
	Text   string
}

// PostMessage stores a message and tries to deliver it right away.
func (b *Board) PostMessage(o PostOpts) (*Message, error) {
	if strings.TrimSpace(o.Text) == "" {
		return nil, errors.New("empty message")
	}
	if o.Kind == "" {
		o.Kind = "note"
	}
	if !msgKinds[o.Kind] {
		return nil, fmt.Errorf("unknown message kind %q (note, work, review)", o.Kind)
	}
	if o.From == "" {
		o.From = "you"
	}
	var to, item any
	if o.To != "" {
		a, err := b.Agent(o.To)
		if err != nil {
			return nil, err
		}
		to = a.ID
		if a.ItemID != nil {
			item = *a.ItemID
		}
	}
	if o.ItemID != 0 {
		it, err := b.Item(o.ItemID)
		if err != nil {
			return nil, err
		}
		item = it.ID
		if to == nil && it.AgentID != nil {
			to = *it.AgentID
		}
	}
	if to == nil && item == nil {
		return nil, errors.New("message needs --to AGENT or --item ID")
	}
	res, err := b.DB.Exec(`INSERT INTO messages("from", to_agent_id, item_id, kind, text, created) VALUES(?,?,?,?,?,?)`,
		o.From, to, item, o.Kind, o.Text, b.now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	var aid, iid *int64
	if v, ok := to.(int64); ok {
		aid = &v
	}
	if v, ok := item.(int64); ok {
		iid = &v
	}
	b.Event("msg.post", aid, iid, map[string]any{"message_id": id, "from": o.From, "kind": o.Kind})
	if aid != nil {
		if _, err := b.deliver(*aid); err != nil {
			b.Event("msg.deliver_error", aid, iid, map[string]any{"error": err.Error()})
		}
	}
	return b.Message(id)
}

func (b *Board) Message(id int64) (*Message, error) {
	m, err := scanMsg(b.DB.QueryRow(msgSel+` WHERE m.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("message %d: %w", id, ErrNotFound)
	}
	return m, err
}

// Messages lists messages, optionally filtered by recipient ref and status.
func (b *Board) Messages(to, status string) ([]*Message, error) {
	q, args := msgSel+` WHERE 1=1`, []any{}
	if to != "" {
		a, err := b.Agent(to)
		if err != nil {
			return nil, err
		}
		q += ` AND m.to_agent_id = ?`
		args = append(args, a.ID)
	}
	if status != "" {
		q += ` AND m.status = ?`
		args = append(args, status)
	}
	rows, err := b.DB.Query(q+` ORDER BY m.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Message{}
	for rows.Next() {
		m, err := scanMsg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AckMessage marks a message delivered by a client (e.g. typed into an attached terminal).
func (b *Board) AckMessage(id int64) (*Message, error) {
	m, err := b.Message(id)
	if err != nil {
		return nil, err
	}
	if _, err := b.DB.Exec(`UPDATE messages SET status = 'delivered', delivered = ? WHERE id = ? AND status = 'pending'`, b.now(), id); err != nil {
		return nil, err
	}
	b.Event("msg.ack", m.ToAgentID, m.ItemID, map[string]any{"message_id": id})
	return b.Message(id)
}

func (b *Board) pending(agentID int64) ([]*Message, error) {
	rows, err := b.DB.Query(msgSel+` WHERE m.to_agent_id = ? AND m.status = 'pending' ORDER BY m.id`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m, err := scanMsg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (b *Board) markDelivered(x execer, msgs []*Message) error {
	now := b.now()
	for _, m := range msgs {
		if _, err := x.Exec(`UPDATE messages SET status = 'delivered', delivered = ? WHERE id = ?`, now, m.ID); err != nil {
			return err
		}
	}
	return nil
}

// combine turns pending messages into one prompt.
func combine(msgs []*Message) string {
	if len(msgs) == 1 {
		return msgs[0].Text
	}
	parts := make([]string, len(msgs))
	for i, m := range msgs {
		parts[i] = fmt.Sprintf("Message %d of %d, from %s (%s):\n\n%s", i+1, len(msgs), m.From, m.Kind, m.Text)
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// Deliver resumes an idle or stopped, unattached agent with its pending
// messages. Busy or attached agents keep them pending. Returns how many
// messages were delivered.
func (b *Board) Deliver(ref string) (int, error) {
	a, err := b.Agent(ref)
	if err != nil {
		return 0, err
	}
	return b.deliver(a.ID)
}

func (b *Board) deliver(id int64) (int, error) {
	a, err := b.AgentByID(id)
	if err != nil {
		return 0, err
	}
	if a.Attached || (a.Status != StatusIdle && a.Status != StatusStopped) {
		return 0, nil
	}
	msgs, err := b.pending(id)
	if err != nil || len(msgs) == 0 {
		return 0, err
	}
	// claim: only one deliverer wins the idle/stopped → working flip
	res, err := b.DB.Exec(`UPDATE agents SET status = 'working', updated = ? WHERE id = ? AND status = ? AND attached = 0`, b.now(), id, a.Status)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, nil
	}
	fail := func(err error) (int, error) {
		b.setStatus(id, a.Status, nil)
		return 0, err
	}
	if a.Status == StatusStopped {
		if err := b.reacquire(a); err != nil {
			return fail(err)
		}
	}
	if a.BgID != "" {
		b.Exec.Claude(a.Dir, b.agentEnv(a), "stop", a.BgID) // best effort: it may already be gone
	}
	_, err = b.Exec.Claude(a.Dir, b.agentEnv(a), "--bg", "--resume", a.SessionID, combine(msgs))
	if err != nil {
		return fail(err)
	}
	tx, err := b.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE agents SET bg_id = ?, updated = ? WHERE id = ?`, bgID(a.SessionID), b.now(), id); err != nil {
		return 0, err
	}
	if err := b.markDelivered(tx, msgs); err != nil {
		return 0, err
	}
	ids := make([]int64, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	if err := b.event(tx, "msg.deliver", &id, a.ItemID, map[string]any{"message_ids": ids, "resumed": true}); err != nil {
		return 0, err
	}
	return len(msgs), tx.Commit()
}

// reacquire re-takes a stopped agent's slot and branch lock before resuming it.
func (b *Board) reacquire(a *Agent) error {
	if a.SlotID == nil {
		return fmt.Errorf("agent %s has no slot", a.Name)
	}
	tx, err := b.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := b.leaseTx(tx, a.Slot, a.ID); err != nil {
		return fmt.Errorf("can't resume %s: %w", a.Name, err)
	}
	if a.Branch != "" {
		if err := b.lockTx(tx, b.repoKey(a.Repo, a.Dir), a.Branch, *a.SlotID, a.ID); err != nil {
			return fmt.Errorf("can't resume %s: %w", a.Name, err)
		}
		if cur, err := b.Exec.Git(a.Dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && cur != a.Branch {
			if err := b.checkout(a.Dir, a.Branch); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
