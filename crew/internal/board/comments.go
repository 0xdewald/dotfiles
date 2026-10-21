package board

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Comment is a line comment on code or a document; same shape as review.lua's.
type Comment struct {
	ID         int64    `json:"id"`
	ItemID     *int64   `json:"item_id"`
	DocumentID *int64   `json:"document_id"`
	ParentID   *int64   `json:"parent_id"`
	Repo       string   `json:"repo"`
	Branch     string   `json:"branch"`
	Commit     string   `json:"commit"`
	File       string   `json:"file"`
	StartLine  int      `json:"start_line"`
	EndLine    int      `json:"end_line"`
	Excerpt    []string `json:"excerpt"`
	Text       string   `json:"text"`
	Author     string   `json:"author"`
	Status     string   `json:"status"`
	Created    string   `json:"created"`
}

const commentSel = `SELECT id, item_id, document_id, parent_id, repo, branch, "commit", file, start_line, end_line,
	excerpt_json, text, author, status, created FROM comments`

func scanComment(s interface{ Scan(...any) error }) (*Comment, error) {
	var c Comment
	var ex string
	if err := s.Scan(&c.ID, &c.ItemID, &c.DocumentID, &c.ParentID, &c.Repo, &c.Branch, &c.Commit, &c.File,
		&c.StartLine, &c.EndLine, &ex, &c.Text, &c.Author, &c.Status, &c.Created); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(ex), &c.Excerpt)
	if c.Excerpt == nil {
		c.Excerpt = []string{}
	}
	return &c, nil
}

var commentStatuses = map[string]bool{"open": true, "sent": true, "resolved": true}

// AddComments stores comments. A comment with a branch but no item is
// attached to the item on that branch (if one exists).
func (b *Board) AddComments(cs []Comment) ([]*Comment, error) {
	tx, err := b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var ids []int64
	for _, c := range cs {
		if strings.TrimSpace(c.Text) == "" {
			return nil, errors.New("comment text is empty")
		}
		if c.Status == "" {
			c.Status = "open"
		}
		if !commentStatuses[c.Status] {
			return nil, fmt.Errorf("unknown comment status %q", c.Status)
		}
		if c.EndLine < c.StartLine {
			c.EndLine = c.StartLine
		}
		if c.ItemID == nil && c.Branch != "" {
			var id int64
			q, args := `SELECT id FROM work_items WHERE branch = ?`, []any{c.Branch}
			if c.Repo != "" {
				q += ` AND (repo = ? OR repo = '')`
				args = append(args, c.Repo)
			}
			if tx.QueryRow(q+` ORDER BY updated DESC LIMIT 1`, args...).Scan(&id) == nil {
				c.ItemID = &id
			}
		}
		if c.Excerpt == nil {
			c.Excerpt = []string{}
		}
		ex, _ := json.Marshal(c.Excerpt)
		res, err := tx.Exec(`INSERT INTO comments(item_id, document_id, parent_id, repo, branch, "commit", file, start_line, end_line,
			excerpt_json, text, author, status, created) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.ItemID, c.DocumentID, c.ParentID, c.Repo, c.Branch, c.Commit, c.File, c.StartLine, c.EndLine,
			string(ex), c.Text, c.Author, c.Status, b.now())
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
		if err := b.event(tx, "comment.add", nil, c.ItemID, map[string]any{"comment_id": id, "file": c.File, "branch": c.Branch}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := make([]*Comment, 0, len(ids))
	for _, id := range ids {
		c, err := b.Comment(id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (b *Board) Comment(id int64) (*Comment, error) {
	c, err := scanComment(b.DB.QueryRow(commentSel+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("comment %d: %w", id, ErrNotFound)
	}
	return c, err
}

// CommentFilter narrows Comments; zero values match everything.
type CommentFilter struct {
	ItemID     int64
	DocumentID int64
	Branch     string
	Repo       string
	Status     string
}

func (b *Board) Comments(f CommentFilter) ([]*Comment, error) {
	q, args := commentSel+` WHERE 1=1`, []any{}
	if f.ItemID != 0 {
		q += ` AND item_id = ?`
		args = append(args, f.ItemID)
	}
	if f.DocumentID != 0 {
		q += ` AND document_id = ?`
		args = append(args, f.DocumentID)
	}
	if f.Branch != "" {
		q += ` AND branch = ?`
		args = append(args, f.Branch)
	}
	if f.Repo != "" {
		q += ` AND repo = ?`
		args = append(args, f.Repo)
	}
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, f.Status)
	}
	rows, err := b.DB.Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Comment{}
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCommentStatus changes a comment's status (open|sent|resolved).
func (b *Board) SetCommentStatus(id int64, status string) (*Comment, error) {
	if !commentStatuses[status] {
		return nil, fmt.Errorf("unknown comment status %q", status)
	}
	c, err := b.Comment(id)
	if err != nil {
		return nil, err
	}
	if _, err := b.DB.Exec(`UPDATE comments SET status = ? WHERE id = ?`, status, id); err != nil {
		return nil, err
	}
	b.Event("comment."+status, nil, c.ItemID, map[string]any{"comment_id": id})
	return b.Comment(id)
}

// SendComments formats comments as one review message (like review.lua's
// M.format) to the agent owning their branch, and marks them sent.
func (b *Board) SendComments(cs []*Comment, from string) (*Message, error) {
	if len(cs) == 0 {
		return nil, errors.New("no comments to send")
	}
	c0 := cs[0]
	var a *Agent
	var err error
	if c0.ItemID != nil {
		if it, ierr := b.Item(*c0.ItemID); ierr == nil && it.AgentID != nil {
			a, err = b.AgentByID(*it.AgentID)
		}
	}
	if a == nil && err == nil {
		a, err = b.AgentByBranch(c0.Branch, c0.Repo)
	}
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "I reviewed your changes and left %d comment%s. Please address each one:", len(cs), plural(len(cs)))
	for i, c := range cs {
		rng := fmt.Sprint(c.StartLine)
		if c.EndLine > c.StartLine {
			rng = fmt.Sprintf("%d-%d", c.StartLine, c.EndLine)
		}
		fmt.Fprintf(&sb, "\n\n%d. `%s:%s`\n```%s\n", i+1, c.File, rng, fence(c.File))
		for _, l := range c.Excerpt {
			sb.WriteString(l + "\n")
		}
		sb.WriteString("```\n" + c.Text)
	}
	if from == "" {
		from = c0.Author
	}
	m, err := b.PostMessage(PostOpts{To: a.Name, Kind: "review", From: from, Text: sb.String()})
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		b.DB.Exec(`UPDATE comments SET status = 'sent' WHERE id = ? AND status = 'open'`, c.ID)
	}
	return m, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func fence(file string) string {
	ext := strings.TrimPrefix(filepath.Ext(file), ".")
	switch ext {
	case "ts", "tsx", "js", "jsx", "go", "py", "lua", "rb", "rs", "sql", "sh", "md", "json", "yaml", "yml", "css", "html":
		return ext
	}
	return ""
}

// Document is a versioned file (plan, review, note) for an item.
type Document struct {
	ID      int64  `json:"id"`
	ItemID  int64  `json:"item_id"`
	Kind    string `json:"kind"`
	Version int    `json:"version"`
	Path    string `json:"path"`
	Created string `json:"created"`
	Content string `json:"content,omitempty"`
}

var docKinds = map[string]bool{"plan": true, "plan-review": true, "code-review": true, "note": true}

// AddDocument stores content as docs/<item>/<kind>-v<n>.md.
func (b *Board) AddDocument(itemID int64, kind string, content []byte) (*Document, error) {
	if !docKinds[kind] {
		return nil, fmt.Errorf("unknown document kind %q (plan, plan-review, code-review, note)", kind)
	}
	if _, err := b.Item(itemID); err != nil {
		return nil, err
	}
	tx, err := b.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var v int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(version),0)+1 FROM documents WHERE item_id = ? AND kind = ?`, itemID, kind).Scan(&v); err != nil {
		return nil, err
	}
	dir := filepath.Join(b.Home, "docs", fmt.Sprint(itemID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-v%d.md", kind, v))
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return nil, err
	}
	res, err := tx.Exec(`INSERT INTO documents(item_id, kind, version, path, created) VALUES(?,?,?,?,?)`, itemID, kind, v, path, b.now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := b.event(tx, "doc.add", nil, &itemID, map[string]any{"document_id": id, "kind": kind, "version": v}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.Document(id, false)
}

func (b *Board) Document(id int64, withContent bool) (*Document, error) {
	var d Document
	err := b.DB.QueryRow(`SELECT id, item_id, kind, version, path, created FROM documents WHERE id = ?`, id).
		Scan(&d.ID, &d.ItemID, &d.Kind, &d.Version, &d.Path, &d.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("document %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if withContent {
		c, err := os.ReadFile(d.Path)
		if err != nil {
			return nil, err
		}
		d.Content = string(c)
	}
	return &d, nil
}

func (b *Board) Documents(itemID int64) ([]*Document, error) {
	rows, err := b.DB.Query(`SELECT id, item_id, kind, version, path, created FROM documents WHERE item_id = ? ORDER BY kind, version`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Document{}
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.ItemID, &d.Kind, &d.Version, &d.Path, &d.Created); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}
