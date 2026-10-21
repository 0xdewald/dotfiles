// Package jira extracts issue keys from free text and fetches issues via REST v3.
package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var keyRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)

// ExtractKey returns the first Jira key in s, or "".
func ExtractKey(s string) string { return keyRe.FindString(s) }

// Client talks to one Jira site.
type Client struct {
	BaseURL, Email, Token string
	HTTP                  *http.Client
}

// FromEnv builds a client from JIRA_BASE_URL/JIRA_EMAIL/JIRA_API_TOKEN as
// returned by get; nil if any is missing.
func FromEnv(get func(string) string) *Client {
	c := &Client{BaseURL: strings.TrimRight(get("JIRA_BASE_URL"), "/"), Email: get("JIRA_EMAIL"), Token: get("JIRA_API_TOKEN")}
	if c.BaseURL == "" || c.Email == "" || c.Token == "" {
		return nil
	}
	return c
}

// Fetch returns the issue summary and the raw issue JSON.
func (c *Client) Fetch(key string) (summary string, raw []byte, err error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/rest/api/3/issue/"+key, nil)
	if err != nil {
		return "", nil, err
	}
	req.SetBasicAuth(c.Email, c.Token)
	req.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("jira %s: %s", key, resp.Status)
	}
	var v struct {
		Fields struct {
			Summary string `json:"summary"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", nil, fmt.Errorf("jira %s: %w", key, err)
	}
	return v.Fields.Summary, raw, nil
}
