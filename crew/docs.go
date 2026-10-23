// Package crew holds crew's own documentation, built into the binary so the dashboard can show
// the guide for exactly the build that serves it.
package crew

import "embed"

//go:embed TRAINING.md README.md ALTERNATIVES.md
var docs embed.FS

// Guide is one document: Name is its file name, Title what the dashboard calls it.
type Guide struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Text  string `json:"text,omitempty"`
}

// Guides lists the documents in reading order.
var Guides = []Guide{
	{Name: "TRAINING.md", Title: "Training"},
	{Name: "README.md", Title: "Reference"},
	{Name: "ALTERNATIVES.md", Title: "Alternatives"},
}

// GuideText is a document's markdown; false if there's no such document.
func GuideText(name string) (string, bool) {
	for _, g := range Guides {
		if g.Name == name {
			raw, err := docs.ReadFile(name)
			return string(raw), err == nil
		}
	}
	return "", false
}
