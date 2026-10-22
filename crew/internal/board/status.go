package board

import (
	"errors"
	"fmt"
)

// Status is a board overview.
type Status struct {
	Stages map[string]int `json:"stages"`
	Agents []*Agent       `json:"agents"`
	Slots  []*Slot        `json:"slots"`
	Server any            `json:"server,omitempty"` // set by the CLI: whether crew serve is running
}

func (b *Board) Status() (*Status, error) {
	s := &Status{Stages: map[string]int{}}
	rows, err := b.DB.Query(`SELECT stage, COUNT(*) FROM work_items GROUP BY stage`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		s.Stages[st] = n
	}
	if s.Agents, err = b.Agents(); err != nil {
		return nil, err
	}
	if s.Slots, err = b.Slots(); err != nil {
		return nil, err
	}
	return s, nil
}

// ReportDone records an agent's summary and moves its item coding → pushed,
// but only once the branch exists on origin.
func (b *Board) ReportDone(agentID int64, summary string) (*Item, error) {
	a, err := b.AgentByID(agentID)
	if err != nil {
		return nil, err
	}
	if a.ItemID == nil {
		return nil, fmt.Errorf("agent %s has no work item", a.Name)
	}
	it, err := b.Item(*a.ItemID)
	if err != nil {
		return nil, err
	}
	if _, err := b.DB.Exec(`UPDATE agents SET last_message = ?, updated = ? WHERE id = ?`, summary, b.now(), a.ID); err != nil {
		return nil, err
	}
	b.Event("agent.report_done", &a.ID, &it.ID, map[string]any{"summary": truncate(summary, 500)})
	if it.Stage != "coding" {
		return it, nil
	}
	out, err := b.Exec.Git(a.Dir, "ls-remote", "--heads", "origin", it.Branch)
	if err != nil {
		return nil, fmt.Errorf("checking origin for %s: %w", it.Branch, err)
	}
	if out == "" {
		return nil, errors.New("branch " + it.Branch + " is not on origin: push it first (git push -u origin " + it.Branch + ")")
	}
	return b.MoveItem(it.ID, "pushed", false)
}
