package board

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// StatusStale: idle (or waiting for input) for longer than StaleAfter. Its background session is
// stopped and it keeps its slot, branch and conversation, but nothing resumes it (messages wait)
// until it's revived.
const StatusStale = "stale"

// StaleAfter is how long an agent may sit idle before it goes stale: $CREW_STALE_AFTER (a Go
// duration like 90m or 4h), default 2h.
func StaleAfter() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("CREW_STALE_AFTER")); err == nil && d > 0 {
		return d
	}
	return 2 * time.Hour
}

// Background runs the agent's conversation as a background session again, after a client (Neovim)
// stopped showing it. Pending messages go with it; cont adds "continue where you left off" for an
// agent that was interrupted mid-turn. Also used to revive stale or stopped agents.
func (b *Board) Background(ref string, cont bool, prompt string) (*Agent, error) {
	a, err := b.Agent(ref)
	if err != nil {
		return nil, err
	}
	if a.Status == StatusStopped {
		if err := b.reacquire(a); err != nil {
			return nil, err
		}
	}
	msgs, err := b.pending(a.ID)
	if err != nil {
		return nil, err
	}
	parts := []string{}
	if cont {
		parts = append(parts, "You were interrupted. Continue where you left off.")
	}
	if prompt != "" {
		parts = append(parts, prompt)
	}
	if len(msgs) > 0 {
		parts = append(parts, combine(msgs))
	}
	text := strings.Join(parts, "\n\n")
	if a.BgID != "" {
		b.Exec.Claude(a.Dir, b.agentEnv(a), "stop", a.BgID) // best effort: make sure only one copy runs
	}
	sid, bg, err := b.runBackground(a, text)
	if err != nil {
		return nil, err
	}
	status := StatusIdle
	if text != "" {
		status = StatusWorking
	}
	tx, err := b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE agents SET session_id = ?, bg_id = ?, status = ?, attached = 0, updated = ? WHERE id = ?`,
		sid, bg, status, b.now(), a.ID); err != nil {
		return nil, err
	}
	if err := b.markDelivered(tx, msgs); err != nil {
		return nil, err
	}
	if err := b.event(tx, "agent.background", &a.ID, a.ItemID, map[string]any{
		"from": a.Status, "continue": cont, "messages": len(msgs), "bg_id": bg}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.AgentByID(a.ID)
}

// Revive brings a stale or stopped agent back: its conversation runs in the background again,
// with prompt and any messages that waited for it.
func (b *Board) Revive(ref, prompt string) (*Agent, error) {
	a, err := b.Agent(ref)
	if err != nil {
		return nil, err
	}
	if a.Status != StatusStale && a.Status != StatusStopped {
		return nil, fmt.Errorf("%s is %s, not stale or stopped", a.Name, a.Status)
	}
	return b.Background(ref, false, prompt)
}

// Sweep marks agents that have been idle or waiting longer than StaleAfter as stale and stops
// their background sessions. Attached agents (open in a client) never go stale.
func (b *Board) Sweep() ([]*Agent, error) {
	cutoff := b.Now().UTC().Add(-StaleAfter()).Format(time.RFC3339Nano)
	rows, err := b.DB.Query(`SELECT id FROM agents WHERE attached = 0 AND status IN (?, ?) AND updated < ?`,
		StatusIdle, StatusNeedsInput, cutoff)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var stale []*Agent
	for _, id := range ids {
		a, err := b.AgentByID(id)
		if err != nil {
			continue
		}
		if a.BgID != "" {
			b.Exec.Claude(a.Dir, b.agentEnv(a), "stop", a.BgID)
		}
		b.DB.Exec(`UPDATE agents SET status = ?, bg_id = '', updated = ? WHERE id = ?`, StatusStale, b.now(), id)
		b.Event("agent.stale", &id, a.ItemID, map[string]any{"idle_since": a.Updated})
		if a, err = b.AgentByID(id); err == nil {
			stale = append(stale, a)
		}
	}
	return stale, nil
}

// RemoveAgent deletes an agent from the board. A running one (anything but stopped) needs force,
// which stops it first. Its work item goes back to queued and pending messages back to the item.
// The conversation itself stays in Claude Code.
func (b *Board) RemoveAgent(ref string, force bool) (*Agent, error) {
	a, err := b.Agent(ref)
	if err != nil {
		return nil, err
	}
	if a.Status != StatusStopped {
		if !force {
			return nil, fmt.Errorf("%s is %s; stop it first (crew agent stop %d) or use --force", a.Name, a.Status, a.ID)
		}
		if a, err = b.StopAgent(ref); err != nil {
			return nil, err
		}
	}
	tx, err := b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`UPDATE messages SET to_agent_id = NULL WHERE to_agent_id = ? AND status = 'pending'`,
		`UPDATE work_items SET stage = CASE stage WHEN 'coding' THEN 'queued' ELSE stage END, agent_id = NULL WHERE agent_id = ?`,
		`UPDATE slots SET leased_by = NULL WHERE leased_by = ?`,
		`DELETE FROM branch_locks WHERE agent_id = ?`,
		`DELETE FROM agents WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, a.ID); err != nil {
			return nil, err
		}
	}
	if err := b.event(tx, "agent.rm", &a.ID, a.ItemID, map[string]any{"name": a.Name}); err != nil {
		return nil, err
	}
	return a, tx.Commit()
}

// RemoveAll removes every stopped agent (with force: every agent, stopping running ones first).
func (b *Board) RemoveAll(force bool) ([]*Agent, error) {
	as, err := b.Agents()
	if err != nil {
		return nil, err
	}
	var removed []*Agent
	for _, a := range as {
		if a.Status != StatusStopped && !force {
			continue
		}
		r, err := b.RemoveAgent(fmt.Sprint(a.ID), force)
		if err != nil {
			return removed, err
		}
		removed = append(removed, r)
	}
	return removed, nil
}
