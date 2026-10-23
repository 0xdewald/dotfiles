// Package api serves the board over HTTP/JSON with an SSE event stream.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/0xdewald/dotfiles/crew"
	"github.com/0xdewald/dotfiles/crew/internal/board"
)

// Build is the crew build serving the API, shown on the dashboard (set by crew serve).
var Build = "dev"

// Handler returns the HTTP API for b, and the dashboard at /.
func Handler(b *board.Board) http.Handler {
	s := &server{b: b, poll: 500 * time.Millisecond}
	m := http.NewServeMux()
	m.HandleFunc("GET /api/status", s.wrap(func(r *http.Request) (any, error) { return b.Status() }))
	m.HandleFunc("GET /api/items", s.wrap(func(r *http.Request) (any, error) { return b.Items(r.URL.Query().Get("stage")) }))
	m.HandleFunc("GET /api/items/{id}", s.wrap(func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		return b.Item(id)
	}))
	m.HandleFunc("POST /api/items/{id}/move", s.wrap(func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			Stage string `json:"stage"`
			Force bool   `json:"force"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return b.MoveItem(id, in.Stage, in.Force)
	}))
	m.HandleFunc("GET /api/agents", s.wrap(func(r *http.Request) (any, error) { return b.Agents() }))
	m.HandleFunc("GET /api/agents/by-branch", s.wrap(func(r *http.Request) (any, error) {
		q := r.URL.Query()
		if q.Get("branch") == "" {
			return nil, badRequest("branch is required")
		}
		return b.AgentByBranch(q.Get("branch"), q.Get("repo"))
	}))
	m.HandleFunc("GET /api/agents/{ref}", s.wrap(func(r *http.Request) (any, error) { return b.Agent(r.PathValue("ref")) }))
	m.HandleFunc("POST /api/agents/{ref}/attach", s.wrap(func(r *http.Request) (any, error) {
		var in struct {
			On bool `json:"on"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return b.SetAttached(r.PathValue("ref"), in.On)
	}))
	m.HandleFunc("POST /api/agents/{ref}/deliver", s.wrap(func(r *http.Request) (any, error) {
		n, err := b.Deliver(r.PathValue("ref"))
		return map[string]int{"delivered": n}, err
	}))
	m.HandleFunc("GET /api/slots", s.wrap(func(r *http.Request) (any, error) { return b.Slots() }))
	m.HandleFunc("GET /api/messages", s.wrap(func(r *http.Request) (any, error) {
		return b.Messages(r.URL.Query().Get("to"), r.URL.Query().Get("status"))
	}))
	m.HandleFunc("POST /api/messages", s.wrap(func(r *http.Request) (any, error) {
		var in struct {
			To     string `json:"to"`
			ItemID int64  `json:"item_id"`
			Kind   string `json:"kind"`
			From   string `json:"from"`
			Text   string `json:"text"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return b.PostMessage(board.PostOpts{To: in.To, ItemID: in.ItemID, Kind: in.Kind, From: in.From, Text: in.Text})
	}))
	m.HandleFunc("POST /api/messages/{id}/ack", s.wrap(func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		return b.AckMessage(id)
	}))
	m.HandleFunc("GET /api/decisions", s.wrap(func(r *http.Request) (any, error) {
		q := r.URL.Query()
		return b.Decisions(board.DecisionFilter{Agent: q.Get("agent"), Status: q.Get("status")})
	}))
	m.HandleFunc("GET /api/decisions/{id}", s.wrap(func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		return b.Decision(id)
	}))
	m.HandleFunc("POST /api/decisions/{id}/answer", s.wrap(func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			Answer string `json:"answer"`
			Accept bool   `json:"accept"`
			Reject bool   `json:"reject"` // answer is then the optional reason
			By     string `json:"by"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		var out struct {
			Decision *board.Decision `json:"decision"`
			Message  *board.Message  `json:"message"`
		}
		switch {
		case in.Accept && in.Reject:
			return nil, errors.New("accept or reject, not both")
		case in.Accept:
			out.Decision, out.Message, err = b.AcceptDecision(id, in.By)
		case in.Reject:
			out.Decision, out.Message, err = b.RejectDecision(id, in.Answer, in.By)
		default:
			out.Decision, out.Message, err = b.AnswerDecision(id, in.Answer, in.By)
		}
		return out, err
	}))
	m.HandleFunc("POST /api/decisions/{id}/cancel", s.wrap(func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		var out struct {
			Decision *board.Decision `json:"decision"`
			Message  *board.Message  `json:"message"`
		}
		out.Decision, out.Message, err = b.CancelDecision(id)
		return out, err
	}))
	m.HandleFunc("GET /api/comments", s.wrap(func(r *http.Request) (any, error) {
		q := r.URL.Query()
		f := board.CommentFilter{Branch: q.Get("branch"), Repo: q.Get("repo"), Status: q.Get("status")}
		f.ItemID, _ = strconv.ParseInt(q.Get("item"), 10, 64)
		f.DocumentID, _ = strconv.ParseInt(q.Get("document"), 10, 64)
		return b.Comments(f)
	}))
	m.HandleFunc("POST /api/comments", s.wrap(func(r *http.Request) (any, error) {
		// body: a comment object, an array of them, or {"comments": [...], "send": true}
		var raw json.RawMessage
		if err := decode(r, &raw); err != nil {
			return nil, err
		}
		var in struct {
			Comments []board.Comment `json:"comments"`
			Send     bool            `json:"send"`
		}
		trim := strings.TrimSpace(string(raw))
		switch {
		case strings.HasPrefix(trim, "["):
			if err := json.Unmarshal(raw, &in.Comments); err != nil {
				return nil, badRequest(err.Error())
			}
		case strings.Contains(trim, `"comments"`):
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, badRequest(err.Error())
			}
		default:
			var c board.Comment
			if err := json.Unmarshal(raw, &c); err != nil {
				return nil, badRequest(err.Error())
			}
			in.Comments = []board.Comment{c}
		}
		added, err := b.AddComments(in.Comments)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		out := map[string]any{"comments": added}
		if in.Send {
			m, err := b.SendComments(added, "")
			if err != nil {
				return nil, err
			}
			out["message"] = m
		}
		return out, nil
	}))
	m.HandleFunc("POST /api/comments/{id}/resolve", s.wrap(func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		return b.SetCommentStatus(id, "resolved")
	}))
	m.HandleFunc("GET /api/docs/{id}", s.wrap(func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		return b.Documents(id)
	}))
	m.HandleFunc("GET /api/doc/{id}", s.wrap(func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		return b.Document(id, true)
	}))
	m.HandleFunc("GET /api/events", s.events)
	// the newest events as JSON (oldest first), for a client's first screen: ?limit=N (default 300)
	m.HandleFunc("GET /api/log", s.wrap(func(r *http.Request) (any, error) {
		limit := 300
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
			limit = min(n, 5000)
		}
		last, err := b.LastEventID()
		if err != nil {
			return nil, err
		}
		return b.Events(max(0, last-int64(limit)), limit)
	}))
	m.HandleFunc("GET /api/version", s.wrap(func(r *http.Request) (any, error) {
		return map[string]string{"build": Build}, nil
	}))
	m.HandleFunc("GET /api/guide", s.wrap(func(r *http.Request) (any, error) { return crew.Guides, nil }))
	m.HandleFunc("GET /api/guide/{name}", s.wrap(func(r *http.Request) (any, error) {
		text, ok := crew.GuideText(r.PathValue("name"))
		if !ok {
			return nil, httpError{http.StatusNotFound, "no guide " + r.PathValue("name")}
		}
		return crew.Guide{Name: r.PathValue("name"), Text: text}, nil
	}))
	m.HandleFunc("GET /{$}", dashboard)
	return m
}

type server struct {
	b    *board.Board
	poll time.Duration
}

type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func badRequest(msg string) error { return httpError{http.StatusBadRequest, msg} }

func (s *server) wrap(f func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := f(r)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			code := http.StatusInternalServerError
			var he httpError
			switch {
			case errors.As(err, &he):
				code = he.code
			case errors.Is(err, board.ErrNotFound):
				code = http.StatusNotFound
			case r.Method == "POST":
				code = http.StatusConflict // refused by board rules
			}
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(v)
	}
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, badRequest("bad id " + r.PathValue("id"))
	}
	return id, nil
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return badRequest("bad JSON body: " + err.Error())
	}
	return nil
}

// events streams new event rows as SSE. ?since=ID replays from there;
// default is "from now". Last-Event-ID is honoured on reconnect. Each event is
// named by its type; ?unnamed=1 sends them unnamed instead, so a browser's
// onmessage gets every type (the dashboard).
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if err != nil {
		if since, err = strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err != nil {
			since, _ = s.b.LastEventID()
		}
	}
	unnamed := r.URL.Query().Get("unnamed") == "1"
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, ": crew events\n\n")
	fl.Flush()
	t := time.NewTicker(s.poll)
	defer t.Stop()
	for {
		evs, err := s.b.Events(since, 500)
		if err != nil {
			return
		}
		for _, e := range evs {
			j, _ := json.Marshal(e)
			if unnamed {
				fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.ID, j)
			} else {
				fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, j)
			}
			since = e.ID
		}
		if len(evs) > 0 {
			fl.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
		}
	}
}
