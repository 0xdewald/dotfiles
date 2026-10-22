// Package board is the crew core: SQLite store, work item lifecycle, slots,
// agents and message routing. CLI, HTTP and MCP all call into it.
package board

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a lookup matches nothing.
var ErrNotFound = errors.New("not found")

// Board is a handle on one crew home.
type Board struct {
	DB   *sql.DB
	Home string
	Exec Exec
	Now  func() time.Time
}

// DefaultHome returns $CREW_HOME or ~/.local/share/crew.
func DefaultHome() string {
	if h := os.Getenv("CREW_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "crew")
}

// Open opens (and migrates) the board in home.
func Open(home string) (*Board, error) {
	if err := os.MkdirAll(filepath.Join(home, "docs"), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(home, "crew.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	b := &Board{DB: db, Home: home, Exec: DefaultExec(), Now: time.Now}
	if err := b.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return b, nil
}

func (b *Board) Close() error { return b.DB.Close() }

func (b *Board) now() string { return b.Now().UTC().Format(time.RFC3339Nano) }

var migrations = []string{
	`CREATE TABLE tickets (
		id INTEGER PRIMARY KEY,
		jira_key TEXT,
		title TEXT NOT NULL DEFAULT '',
		text TEXT NOT NULL DEFAULT '',
		fetched_json TEXT,
		created TEXT NOT NULL
	);
	CREATE INDEX tickets_jira ON tickets(jira_key);
	CREATE TABLE work_items (
		id INTEGER PRIMARY KEY,
		ticket_id INTEGER REFERENCES tickets(id),
		title TEXT NOT NULL DEFAULT '',
		stage TEXT NOT NULL DEFAULT 'new',
		repo TEXT NOT NULL DEFAULT '',
		branch TEXT NOT NULL DEFAULT '',
		agent_id INTEGER,
		rounds INTEGER NOT NULL DEFAULT 0,
		created TEXT NOT NULL,
		updated TEXT NOT NULL
	);
	CREATE TABLE agents (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		role TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL UNIQUE,
		bg_id TEXT NOT NULL DEFAULT '',
		slot_id INTEGER,
		dir TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		attached INTEGER NOT NULL DEFAULT 0,
		last_message TEXT NOT NULL DEFAULT '',
		created TEXT NOT NULL,
		updated TEXT NOT NULL
	);
	CREATE TABLE slots (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		path TEXT NOT NULL UNIQUE,
		port_offset INTEGER NOT NULL,
		leased_by INTEGER
	);
	CREATE TABLE branch_locks (
		repo TEXT NOT NULL,
		branch TEXT NOT NULL,
		slot_id INTEGER NOT NULL,
		agent_id INTEGER NOT NULL,
		UNIQUE(repo, branch)
	);
	CREATE TABLE documents (
		id INTEGER PRIMARY KEY,
		item_id INTEGER NOT NULL,
		kind TEXT NOT NULL,
		version INTEGER NOT NULL,
		path TEXT NOT NULL,
		created TEXT NOT NULL,
		UNIQUE(item_id, kind, version)
	);
	CREATE TABLE comments (
		id INTEGER PRIMARY KEY,
		item_id INTEGER,
		document_id INTEGER,
		parent_id INTEGER,
		repo TEXT NOT NULL DEFAULT '',
		branch TEXT NOT NULL DEFAULT '',
		"commit" TEXT NOT NULL DEFAULT '',
		file TEXT NOT NULL DEFAULT '',
		start_line INTEGER NOT NULL DEFAULT 0,
		end_line INTEGER NOT NULL DEFAULT 0,
		excerpt_json TEXT NOT NULL DEFAULT '[]',
		text TEXT NOT NULL,
		author TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'open',
		created TEXT NOT NULL
	);
	CREATE TABLE messages (
		id INTEGER PRIMARY KEY,
		"from" TEXT NOT NULL DEFAULT '',
		to_agent_id INTEGER,
		item_id INTEGER,
		kind TEXT NOT NULL DEFAULT 'note',
		text TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		created TEXT NOT NULL,
		delivered TEXT
	);
	CREATE TABLE events (
		id INTEGER PRIMARY KEY,
		ts TEXT NOT NULL,
		type TEXT NOT NULL,
		agent_id INTEGER,
		item_id INTEGER,
		data_json TEXT NOT NULL DEFAULT '{}'
	);`,
	// which side of a diff a comment was made on: new (the change) or old (before it)
	`ALTER TABLE comments ADD COLUMN side TEXT NOT NULL DEFAULT 'new';`,
	// questions agents put on the board instead of asking in chat; message_id is the message that
	// took the answers back (answers wait until all of an agent's open decisions are answered)
	`CREATE TABLE decisions (
		id INTEGER PRIMARY KEY,
		agent_id INTEGER NOT NULL,
		item_id INTEGER,
		question TEXT NOT NULL,
		options_json TEXT NOT NULL DEFAULT '[]',
		recommendation TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'open',
		answer TEXT NOT NULL DEFAULT '',
		answered_by TEXT NOT NULL DEFAULT '',
		message_id INTEGER,
		created TEXT NOT NULL,
		answered TEXT
	);
	CREATE INDEX decisions_agent ON decisions(agent_id, status);`,
}

func (b *Board) migrate() error {
	var v int
	if err := b.DB.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := b.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Event is one row of the append-only log.
type Event struct {
	ID      int64           `json:"id"`
	TS      string          `json:"ts"`
	Type    string          `json:"type"`
	AgentID *int64          `json:"agent_id"`
	ItemID  *int64          `json:"item_id"`
	Data    json.RawMessage `json:"data"`
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func (b *Board) event(x execer, typ string, agentID, itemID *int64, data any) error {
	if data == nil {
		data = map[string]any{}
	}
	j, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = x.Exec(`INSERT INTO events(ts, type, agent_id, item_id, data_json) VALUES(?,?,?,?,?)`,
		b.now(), typ, agentID, itemID, string(j))
	return err
}

// Event appends an event row.
func (b *Board) Event(typ string, agentID, itemID *int64, data any) error {
	return b.event(b.DB, typ, agentID, itemID, data)
}

// Events returns events with id > since, oldest first.
func (b *Board) Events(since int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := b.DB.Query(`SELECT id, ts, type, agent_id, item_id, data_json FROM events WHERE id > ? ORDER BY id LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var d string
		if err := rows.Scan(&e.ID, &e.TS, &e.Type, &e.AgentID, &e.ItemID, &d); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(d)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastEventID returns the newest event id (0 if none).
func (b *Board) LastEventID() (int64, error) {
	var id sql.NullInt64
	err := b.DB.QueryRow(`SELECT MAX(id) FROM events`).Scan(&id)
	return id.Int64, err
}

func ptr[T any](v T) *T { return &v }

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
