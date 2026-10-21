package board

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
)

// Slot is a separate checkout directory an agent can lease.
type Slot struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	PortOffset int    `json:"port_offset"`
	LeasedBy   *int64 `json:"leased_by"`
	Agent      string `json:"agent"` // leasing agent's name
}

const slotSel = `SELECT s.id, s.name, s.path, s.port_offset, s.leased_by, COALESCE(a.name,'')
	FROM slots s LEFT JOIN agents a ON a.id = s.leased_by`

func scanSlot(s interface{ Scan(...any) error }) (*Slot, error) {
	var x Slot
	err := s.Scan(&x.ID, &x.Name, &x.Path, &x.PortOffset, &x.LeasedBy, &x.Agent)
	return &x, err
}

// AddSlot registers a slot. portOffset < 0 means 100 × current slot count.
func (b *Board) AddSlot(name, path string, portOffset int) (*Slot, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if portOffset < 0 {
		var n int
		if err := b.DB.QueryRow(`SELECT COUNT(*) FROM slots`).Scan(&n); err != nil {
			return nil, err
		}
		portOffset = 100 * n
	}
	if _, err := b.DB.Exec(`INSERT INTO slots(name, path, port_offset) VALUES(?,?,?)`, name, abs, portOffset); err != nil {
		return nil, fmt.Errorf("slot %s: %w", name, err)
	}
	b.Event("slot.add", nil, nil, map[string]any{"slot": name, "path": abs, "port_offset": portOffset})
	return b.Slot(name)
}

func (b *Board) Slot(name string) (*Slot, error) {
	s, err := scanSlot(b.DB.QueryRow(slotSel+` WHERE s.name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("slot %s: %w", name, ErrNotFound)
	}
	return s, err
}

func (b *Board) slotByID(id int64) (*Slot, error) {
	s, err := scanSlot(b.DB.QueryRow(slotSel+` WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("slot #%d: %w", id, ErrNotFound)
	}
	return s, err
}

func (b *Board) Slots() ([]*Slot, error) {
	rows, err := b.DB.Query(slotSel + ` ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Slot{}
	for rows.Next() {
		s, err := scanSlot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// LeaseSlot leases a free slot to an agent (re-leasing to the same agent is fine).
func (b *Board) LeaseSlot(name string, agentID int64) (*Slot, error) {
	tx, err := b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := b.leaseTx(tx, name, agentID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.Slot(name)
}

func (b *Board) leaseTx(tx *sql.Tx, name string, agentID int64) error {
	var id int64
	var by sql.NullInt64
	if err := tx.QueryRow(`SELECT id, leased_by FROM slots WHERE name = ?`, name).Scan(&id, &by); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("slot %s: %w", name, ErrNotFound)
		}
		return err
	}
	if by.Valid && by.Int64 != agentID {
		return fmt.Errorf("slot %s is leased by agent %d", name, by.Int64)
	}
	if _, err := tx.Exec(`UPDATE slots SET leased_by = ? WHERE id = ?`, agentID, id); err != nil {
		return err
	}
	return b.event(tx, "slot.lease", &agentID, nil, map[string]any{"slot": name})
}

// ReleaseSlot frees a slot and any branch locks held in it.
func (b *Board) ReleaseSlot(name string) (*Slot, error) {
	s, err := b.Slot(name)
	if err != nil {
		return nil, err
	}
	tx, err := b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE slots SET leased_by = NULL WHERE id = ?`, s.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM branch_locks WHERE slot_id = ?`, s.ID); err != nil {
		return nil, err
	}
	if err := b.event(tx, "slot.release", s.LeasedBy, nil, map[string]any{"slot": name}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.Slot(name)
}

// BranchLock records which slot has a repo+branch checked out.
type BranchLock struct {
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	SlotID  int64  `json:"slot_id"`
	AgentID int64  `json:"agent_id"`
}

// lockTx takes the repo+branch lock for slot/agent; refused if another slot holds it.
func (b *Board) lockTx(tx *sql.Tx, repo, branch string, slotID, agentID int64) error {
	var sid int64
	err := tx.QueryRow(`SELECT slot_id FROM branch_locks WHERE repo = ? AND branch = ?`, repo, branch).Scan(&sid)
	switch {
	case err == nil && sid != slotID:
		var name string
		tx.QueryRow(`SELECT name FROM slots WHERE id = ?`, sid).Scan(&name)
		return fmt.Errorf("branch %s (%s) is locked by slot %s", branch, repo, name)
	case err == nil:
		_, err = tx.Exec(`UPDATE branch_locks SET agent_id = ? WHERE repo = ? AND branch = ?`, agentID, repo, branch)
		return err
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	_, err = tx.Exec(`INSERT INTO branch_locks(repo, branch, slot_id, agent_id) VALUES(?,?,?,?)`, repo, branch, slotID, agentID)
	return err
}

// LockBranch takes a branch lock outside agent start (mostly for tests/tools).
func (b *Board) LockBranch(repo, branch string, slotID, agentID int64) error {
	tx, err := b.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := b.lockTx(tx, repo, branch, slotID, agentID); err != nil {
		return err
	}
	return tx.Commit()
}

func (b *Board) BranchLocks() ([]BranchLock, error) {
	rows, err := b.DB.Query(`SELECT repo, branch, slot_id, agent_id FROM branch_locks ORDER BY repo, branch`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BranchLock{}
	for rows.Next() {
		var l BranchLock
		if err := rows.Scan(&l.Repo, &l.Branch, &l.SlotID, &l.AgentID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
