package tui

import (
	"strings"
	"testing"

	"github.com/tphuc/lazycomd/internal/manager"
)

func openPalette(names ...string) paletteModel {
	p := newPalette()
	p.Open(rows(names...))
	return p
}

func typePalette(p paletteModel, s string) paletteModel {
	for _, r := range s {
		p, _ = p.Update(key(string(r)))
	}
	return p
}

func TestPaletteOpenListsEverything(t *testing.T) {
	p := openPalette("proxy", "app:api", "scraper:api")
	if got, ok := p.Highlighted(); !ok || got.Name != "proxy" {
		t.Fatalf("highlighted = %q, want the first row", got.Name)
	}
	view := p.View(60, 10)
	for _, want := range []string{"proxy", "app:api", "scraper:api"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestPaletteTypingNarrows(t *testing.T) {
	p := openPalette("proxy", "app:api", "scraper:api")
	p = typePalette(p, "api")

	view := p.View(60, 10)
	if strings.Contains(view, "proxy") {
		t.Fatalf("non-matching name still listed:\n%s", view)
	}
	got, ok := p.Highlighted()
	if !ok || got.Name != "app:api" {
		t.Fatalf("highlighted = %q, want app:api (best match)", got.Name)
	}
}

func TestPaletteNavigation(t *testing.T) {
	p := openPalette("a", "b", "c")

	p, _ = p.Update(key("down"))
	if got, _ := p.Highlighted(); got.Name != "b" {
		t.Fatalf("after down: %q, want b", got.Name)
	}
	p, _ = p.Update(key("ctrl+n"))
	if got, _ := p.Highlighted(); got.Name != "c" {
		t.Fatalf("after ctrl+n: %q, want c", got.Name)
	}
	p, _ = p.Update(key("ctrl+n")) // past the end: clamps
	if got, _ := p.Highlighted(); got.Name != "c" {
		t.Fatalf("clamp failed: %q, want c", got.Name)
	}
	p, _ = p.Update(key("up"))
	if got, _ := p.Highlighted(); got.Name != "b" {
		t.Fatalf("after up: %q, want b", got.Name)
	}
	p, _ = p.Update(key("ctrl+p"))
	p, _ = p.Update(key("ctrl+p")) // past the start: clamps
	if got, _ := p.Highlighted(); got.Name != "a" {
		t.Fatalf("clamp failed: %q, want a", got.Name)
	}
}

func TestPaletteCursorClampsWhenMatchesShrink(t *testing.T) {
	p := openPalette("aa", "ab", "ac")
	p, _ = p.Update(key("down"))
	p, _ = p.Update(key("down")) // on "ac"
	p = typePalette(p, "aa")     // only one match left

	got, ok := p.Highlighted()
	if !ok || got.Name != "aa" {
		t.Fatalf("highlighted = %q, ok = %v; want aa", got.Name, ok)
	}
}

func TestPaletteNoMatches(t *testing.T) {
	p := openPalette("a", "b")
	p = typePalette(p, "zzz")
	if _, ok := p.Highlighted(); ok {
		t.Fatal("Highlighted ok = true with no matches")
	}
	if view := p.View(60, 10); !strings.Contains(view, "no match") {
		t.Fatalf("view should say there are no matches:\n%s", view)
	}
}

func TestPaletteCarriesState(t *testing.T) {
	p := newPalette()
	p.Open([]manager.Status{{Name: "tick", State: manager.Running}})
	got, ok := p.Highlighted()
	if !ok || got.State != manager.Running {
		t.Fatalf("highlighted = %+v, want the running status", got)
	}
}

func TestPaletteShowsTypedQuery(t *testing.T) {
	p := openPalette("app:api")
	p = typePalette(p, "api")
	if view := p.View(60, 10); !strings.Contains(view, "api") {
		t.Fatalf("query not rendered:\n%s", view)
	}
}

func TestPaletteViewRespectsHeight(t *testing.T) {
	names := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		names = append(names, string(rune('a'+i%26))+"cmd")
	}
	p := openPalette(names...)
	view := p.View(60, 6)
	if got := len(strings.Split(view, "\n")); got > 6 {
		t.Fatalf("view is %d lines, want <= 6", got)
	}
}
