package board

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0xdewald/dotfiles/crew/internal/names"
)

// startArgs starts a new background session for an agent.
func startArgs(name, mode, model, prompt string) []string {
	args := []string{"--bg", "-n", name, "--permission-mode", mode}
	if model != "" {
		args = append(args, "--model", model)
	}
	if prompt != "" {
		args = append(args, prompt)
	}
	return args
}

// Roles and their claude --permission-mode. Workers run in the background with nobody to answer
// permission prompts, so they use auto mode: routine actions (reading tickets, running tests,
// committing) go ahead, risky ones still ask (the agent shows as needs-input and you're notified).
// The other roles only read and plan. Override a role's mode with CREW_<ROLE>_PERMISSION_MODE.
var Roles = map[string]string{
	"worker":   "auto",
	"techlead": "plan",
	"intern":   "plan",
	"reviewer": "plan",
	"director": "plan",
}

func permissionMode(role string) string {
	if m := os.Getenv("CREW_" + strings.ToUpper(role) + "_PERMISSION_MODE"); m != "" {
		return m
	}
	return Roles[role]
}

// Agent statuses.
const (
	StatusStarting   = "starting"
	StatusWorking    = "working"
	StatusIdle       = "idle"
	StatusNeedsInput = "needs-input"
	StatusStopped    = "stopped"
)

// Agent is a Claude session the board manages. Item fields describe the
// work item it's assigned to (most recently updated), if any.
type Agent struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Model       string `json:"model"`
	SessionID   string `json:"session_id"`
	BgID        string `json:"bg_id"`
	SlotID      *int64 `json:"slot_id"`
	Slot        string `json:"slot"`
	SlotPath    string `json:"slot_path"`
	PortOffset  int    `json:"port_offset"`
	Dir         string `json:"dir"`
	Status      string `json:"status"`
	Attached    bool   `json:"attached"`
	LastMessage string `json:"last_message"`
	ItemID      *int64 `json:"item_id"`
	Stage       string `json:"stage"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	Pending     int    `json:"pending"`
	Created     string `json:"created"`
	Updated     string `json:"updated"`
	// Conversation is the session holding the agent's conversation ("" if none yet); filled by
	// `crew agent show` so clients resume the right one.
	Conversation string `json:"conversation,omitempty"`
}

const agentSel = `SELECT a.id, a.name, a.role, a.model, a.session_id, a.bg_id, a.slot_id, COALESCE(s.name,''),
	COALESCE(s.path,''), COALESCE(s.port_offset,0), a.dir, a.status, a.attached, a.last_message,
	w.id, COALESCE(w.stage,''), COALESCE(w.repo,''), COALESCE(w.branch,''),
	(SELECT COUNT(*) FROM messages m WHERE m.to_agent_id = a.id AND m.status = 'pending'),
	a.created, a.updated
	FROM agents a LEFT JOIN slots s ON s.id = a.slot_id
	LEFT JOIN work_items w ON w.id = (SELECT id FROM work_items WHERE agent_id = a.id ORDER BY updated DESC, id DESC LIMIT 1)`

func scanAgent(s interface{ Scan(...any) error }) (*Agent, error) {
	var a Agent
	err := s.Scan(&a.ID, &a.Name, &a.Role, &a.Model, &a.SessionID, &a.BgID, &a.SlotID, &a.Slot, &a.SlotPath, &a.PortOffset,
		&a.Dir, &a.Status, &a.Attached, &a.LastMessage, &a.ItemID, &a.Stage, &a.Repo, &a.Branch, &a.Pending, &a.Created, &a.Updated)
	return &a, err
}

func (b *Board) agentWhere(where string, args ...any) (*Agent, error) {
	a, err := scanAgent(b.DB.QueryRow(agentSel+" WHERE "+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// Agent finds an agent by numeric id or name (case-insensitive).
func (b *Board) Agent(ref string) (*Agent, error) {
	var a *Agent
	var err error
	if id, perr := strconv.ParseInt(ref, 10, 64); perr == nil {
		a, err = b.agentWhere(`a.id = ?`, id)
	} else {
		a, err = b.agentWhere(`a.name = ? COLLATE NOCASE`, ref)
	}
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("agent %s: %w", ref, ErrNotFound)
	}
	return a, err
}

func (b *Board) AgentByID(id int64) (*Agent, error) { return b.Agent(strconv.FormatInt(id, 10)) }

// AgentBySession finds the agent owning a Claude session id.
func (b *Board) AgentBySession(sid string) (*Agent, error) {
	a, err := b.agentWhere(`a.session_id = ?`, sid)
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("session %s: %w", sid, ErrNotFound)
	}
	return a, err
}

// AgentByBranch finds the agent working on branch (optionally in repo). It
// prefers live agents, then the most recently updated item. Items with no
// repo recorded match any repo.
func (b *Board) AgentByBranch(branch, repo string) (*Agent, error) {
	q := `SELECT w.agent_id FROM work_items w JOIN agents a ON a.id = w.agent_id WHERE w.branch = ?`
	args := []any{branch}
	if repo != "" {
		q += ` AND (w.repo = ? OR w.repo = '')`
		args = append(args, repo)
	}
	q += ` ORDER BY (a.status = 'stopped'), (w.repo = ?) DESC, w.updated DESC LIMIT 1`
	args = append(args, repo)
	var id int64
	if err := b.DB.QueryRow(q, args...).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("agent on branch %s: %w", branch, ErrNotFound)
		}
		return nil, err
	}
	return b.AgentByID(id)
}

// Agents lists all agents, newest first.
func (b *Board) Agents() ([]*Agent, error) {
	rows, err := b.DB.Query(agentSel + ` ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// StartOpts configures StartAgent.
type StartOpts struct {
	ItemID int64
	Slot   string // "" picks a free slot
	Role   string // default worker
	Model  string
	Name   string // "" generates one
	Prompt string // optional first prompt
}

// StartAgent leases a slot, locks and checks out the item's branch, and
// starts a background Claude session there.
func (b *Board) StartAgent(o StartOpts) (*Agent, error) {
	item, err := b.Item(o.ItemID)
	if err != nil {
		return nil, err
	}
	if item.AgentID != nil {
		if cur, err := b.AgentByID(*item.AgentID); err == nil && cur.Status != StatusStopped {
			return nil, fmt.Errorf("item %d already has agent %s (%s)", item.ID, cur.Name, cur.Status)
		}
	}
	if o.Role == "" {
		o.Role = "worker"
	}
	_, ok := Roles[o.Role]
	mode := permissionMode(o.Role)
	if !ok {
		return nil, fmt.Errorf("unknown role %q", o.Role)
	}
	slot, err := b.pickSlot(o.Slot)
	if err != nil {
		return nil, err
	}
	if err := checkSlotDir(slot.Path); err != nil {
		return nil, fmt.Errorf("slot %s: %w", slot.Name, err)
	}
	dir := slot.Path
	if item.Repo != "" {
		if st, err := os.Stat(filepath.Join(slot.Path, item.Repo)); err == nil && st.IsDir() {
			dir = filepath.Join(slot.Path, item.Repo)
		}
	}
	repoKey := b.repoKey(item.Repo, dir)
	var lockSlot int64
	if err := b.DB.QueryRow(`SELECT slot_id FROM branch_locks WHERE repo = ? AND branch = ?`, repoKey, item.Branch).Scan(&lockSlot); err == nil && lockSlot != slot.ID {
		other, _ := b.slotByID(lockSlot)
		return nil, fmt.Errorf("branch %s (%s) is locked by slot %s", item.Branch, repoKey, other.Name)
	}
	if err := b.checkout(dir, item.Branch); err != nil {
		return nil, err
	}

	name := o.Name
	if name == "" {
		name = names.New(func(n string) bool {
			var x int
			return b.DB.QueryRow(`SELECT 1 FROM agents WHERE name = ? COLLATE NOCASE`, n).Scan(&x) == nil
		})
	}
	sid := newUUID()
	now := b.now()

	tx, err := b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO agents(name, role, model, session_id, slot_id, dir, status, created, updated) VALUES(?,?,?,?,?,?,?,?,?)`,
		name, o.Role, o.Model, sid, slot.ID, dir, StatusStarting, now, now)
	if err != nil {
		return nil, fmt.Errorf("agent %s: %w", name, err)
	}
	aid, _ := res.LastInsertId()
	if err := b.leaseTx(tx, slot.Name, aid); err != nil {
		return nil, err
	}
	if err := b.lockTx(tx, repoKey, item.Branch, slot.ID, aid); err != nil {
		return nil, err
	}
	// unaddressed messages for this item become the agent's first prompt
	if _, err := tx.Exec(`UPDATE messages SET to_agent_id = ? WHERE item_id = ? AND to_agent_id IS NULL AND status = 'pending'`, aid, item.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	a, err := b.AgentByID(aid)
	if err != nil {
		return nil, err
	}
	msgs, err := b.pending(aid)
	if err != nil {
		return nil, err
	}
	// the work itself (the ticket) always goes in the first prompt; --prompt adds to it
	prompt := strings.TrimSpace(b.workBrief(item) + "\n\n" + o.Prompt)
	if len(msgs) > 0 {
		prompt = strings.TrimSpace(prompt + "\n\n" + combine(msgs))
	}
	args := startArgs(name, mode, o.Model, prompt)
	var out string
	launched := time.Now()
	out, err = b.Exec.Claude(dir, b.agentEnv(a), args...)
	var bg string
	if err == nil {
		if bg = parseBackgrounded(out); bg == "" {
			err = fmt.Errorf("claude --bg: no session id in its output: %s", strings.TrimSpace(out))
		} else if sid, err = b.awaitBackground(dir, bg, launched); err != nil {
			b.Exec.Claude(dir, nil, "stop", bg)
		}
	}
	if err != nil {
		// It never ran: undo the start completely so it doesn't linger on the board
		b.DB.Exec(`UPDATE messages SET to_agent_id = NULL WHERE to_agent_id = ? AND status = 'pending'`, aid)
		b.DB.Exec(`UPDATE slots SET leased_by = NULL WHERE leased_by = ?`, aid)
		b.DB.Exec(`DELETE FROM branch_locks WHERE agent_id = ?`, aid)
		b.DB.Exec(`DELETE FROM agents WHERE id = ?`, aid)
		b.Event("agent.error", &aid, &item.ID, map[string]any{"error": err.Error()})
		if strings.Contains(err.Error(), "not trusted") {
			return nil, fmt.Errorf("Claude hasn't been allowed to work in %s yet. Allow it once with `crew slot trust %s` "+
				"(accept the prompt, then type /exit), then run `crew agent start %d` again", dir, slot.Name, item.ID)
		}
		return nil, err
	}
	tx, err = b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE agents SET session_id = ?, bg_id = ?, updated = ? WHERE id = ?`, sid, bg, b.now(), aid); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE work_items SET agent_id = ?, updated = ? WHERE id = ?`, aid, b.now(), item.ID); err != nil {
		return nil, err
	}
	if item.Stage == "queued" || item.Stage == "new" {
		if err := b.moveTx(tx, item.ID, "coding", true); err != nil {
			return nil, err
		}
	}
	if err := b.markDelivered(tx, msgs); err != nil {
		return nil, err
	}
	if err := b.event(tx, "agent.start", &aid, &item.ID, map[string]any{
		"name": name, "role": o.Role, "slot": slot.Name, "dir": dir, "branch": item.Branch, "session_id": sid, "bg_id": bg,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.AgentByID(aid)
}

func (b *Board) pickSlot(name string) (*Slot, error) {
	if name != "" {
		s, err := b.Slot(name)
		if err != nil {
			return nil, err
		}
		if s.LeasedBy != nil {
			return nil, fmt.Errorf("slot %s is leased by %s", name, s.Agent)
		}
		return s, nil
	}
	s, err := scanSlot(b.DB.QueryRow(slotSel + ` WHERE s.leased_by IS NULL ORDER BY s.id LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("no free slot (crew slot add NAME PATH)")
	}
	return s, err
}

// repoKey identifies a repo across slots: the item's repo name, else the
// origin URL, else the directory.
func (b *Board) repoKey(repo, dir string) string {
	if repo != "" {
		return repo
	}
	if u, err := b.Exec.Git(dir, "remote", "get-url", "origin"); err == nil && u != "" {
		return u
	}
	return dir
}

func (b *Board) checkout(dir, branch string) error {
	if _, err := b.Exec.Git(dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err = b.Exec.Git(dir, "checkout", branch)
		return err
	}
	if _, err := b.Exec.Git(dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch); err == nil {
		_, err = b.Exec.Git(dir, "checkout", "-b", branch, "--track", "origin/"+branch)
		return err
	}
	_, err := b.Exec.Git(dir, "checkout", "-b", branch)
	return err
}

func (b *Board) agentEnv(a *Agent) []string {
	return []string{
		"CREW_AGENT_ID=" + strconv.FormatInt(a.ID, 10),
		"CREW_AGENT_NAME=" + a.Name,
		"CREW_HOME=" + b.Home,
		"CREW_PORT_OFFSET=" + strconv.Itoa(a.PortOffset),
		"CREW_PROCESS_PREFIX=" + names.Slug(a.Name) + "-",
	}
}

// StopAgent stops the background session and frees its slot and locks.
func (b *Board) StopAgent(ref string) (*Agent, error) {
	a, err := b.Agent(ref)
	if err != nil {
		return nil, err
	}
	data := map[string]any{}
	if a.BgID != "" {
		if _, err := b.Exec.Claude(a.Dir, b.agentEnv(a), "stop", a.BgID); err != nil {
			data["stop_error"] = err.Error()
		}
	}
	tx, err := b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`UPDATE slots SET leased_by = NULL WHERE leased_by = ?`,
		`DELETE FROM branch_locks WHERE agent_id = ?`,
		`UPDATE agents SET status = 'stopped', bg_id = '', attached = 0, updated = '` + b.now() + `' WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, a.ID); err != nil {
			return nil, err
		}
	}
	if err := b.event(tx, "agent.stop", &a.ID, a.ItemID, data); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.AgentByID(a.ID)
}

// SetAttached marks whether a client is showing this agent's terminal; attached
// agents aren't resumed by delivery.
func (b *Board) SetAttached(ref string, on bool) (*Agent, error) {
	a, err := b.Agent(ref)
	if err != nil {
		return nil, err
	}
	if _, err := b.DB.Exec(`UPDATE agents SET attached = ?, updated = ? WHERE id = ?`, on, b.now(), a.ID); err != nil {
		return nil, err
	}
	b.Event("agent.attach", &a.ID, a.ItemID, map[string]any{"attached": on})
	if !on {
		if _, err := b.Deliver(a.Name); err != nil {
			return nil, err
		}
	}
	return b.AgentByID(a.ID)
}

func (b *Board) setStatus(id int64, status string, last *string) error {
	if last != nil {
		_, err := b.DB.Exec(`UPDATE agents SET status = ?, last_message = ?, updated = ? WHERE id = ?`, status, *last, b.now(), id)
		return err
	}
	_, err := b.DB.Exec(`UPDATE agents SET status = ?, updated = ? WHERE id = ?`, status, b.now(), id)
	return err
}

func newUUID() string {
	var u [16]byte
	rand.Read(u[:])
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])
}

// workBrief tells a new agent what its work item is: the item's title, the ticket text, and the
// Jira key to read with its own tools.
func (b *Board) workBrief(it *Item) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Your work item (#%d): %s", it.ID, it.Title)
	if it.TicketID != nil {
		if t, err := b.Ticket(fmt.Sprint(*it.TicketID)); err == nil {
			if t.JiraKey != "" {
				fmt.Fprintf(&sb, "\nJira ticket: %s. Read it in full with your Jira tools before you start.", t.JiraKey)
			}
			if text := strings.TrimSpace(t.Text); text != "" && text != it.Title {
				sb.WriteString("\n\nTicket:\n" + text)
			}
		}
	}
	return sb.String()
}
