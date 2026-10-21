package jira

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractKey(t *testing.T) {
	for in, want := range map[string]string{
		"read jira ticket VD-21582, focus on X": "VD-21582",
		"VD-1":                                  "VD-1",
		"see ab-12 and A2B-7":                   "A2B-7",
		"no key here":                           "",
		"X-1 single letter project":             "",
	} {
		if got := ExtractKey(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "me@x" || p != "tok" || r.URL.Path != "/rest/api/3/issue/VD-1" {
			http.Error(w, "no", 401)
			return
		}
		w.Write([]byte(`{"key":"VD-1","fields":{"summary":"Fix it"}}`))
	}))
	defer srv.Close()
	c := FromEnv(m{"JIRA_BASE_URL": srv.URL + "/", "JIRA_EMAIL": "me@x", "JIRA_API_TOKEN": "tok"}.get)
	s, raw, err := c.Fetch("VD-1")
	if err != nil || s != "Fix it" || len(raw) == 0 {
		t.Fatal(s, err)
	}
	if FromEnv(m{}.get) != nil {
		t.Fatal("expected nil client")
	}
}

type m map[string]string

func (x m) get(k string) string { return x[k] }
