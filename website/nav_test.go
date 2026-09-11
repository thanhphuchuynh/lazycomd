package website

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDocsJSONPagesExist(t *testing.T) {
	raw, err := os.ReadFile("docs.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Name       string `json:"name"`
		Theme      string `json:"theme"`
		Navigation struct {
			Groups []struct {
				Group string   `json:"group"`
				Pages []string `json:"pages"`
			} `json:"groups"`
		} `json:"navigation"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Name != "lazycomd" || doc.Theme != "mint" {
		t.Fatalf("name=%q theme=%q", doc.Name, doc.Theme)
	}
	wantGroups := []string{"Get started", "Use", "Configure", "Reference"}
	if len(doc.Navigation.Groups) != len(wantGroups) {
		t.Fatalf("groups = %d, want %d", len(doc.Navigation.Groups), len(wantGroups))
	}
	var pages []string
	for i, g := range doc.Navigation.Groups {
		if g.Group != wantGroups[i] {
			t.Errorf("group[%d] = %q, want %q", i, g.Group, wantGroups[i])
		}
		pages = append(pages, g.Pages...)
	}
	wantPages := []string{"index", "install", "tui", "cli", "configuration", "api", "behavior"}
	if len(pages) != len(wantPages) {
		t.Fatalf("pages = %v, want %v", pages, wantPages)
	}
	for i, p := range wantPages {
		if pages[i] != p {
			t.Errorf("page[%d] = %q, want %q", i, pages[i], p)
		}
		if _, err := os.Stat(filepath.Join(p + ".mdx")); err != nil {
			t.Errorf("missing %s.mdx: %v", p, err)
		}
	}
}
