# lazycomd Mintlify GitHub Pages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish Mintlify docs to GitHub Pages on each `v*` tag (latest only) and rename the Go module to `github.com/thanhphuchuynh/lazycomd`.

**Architecture:** Mintlify source lives in `website/`. A tag workflow starts `mint dev`, crawls it with staticmint, and deploys the static tree to GitHub Pages, overwriting the previous site. The Go module path and imports move in the same change. Historical `docs/superpowers/` files and the launchd label `com.tphuc.lazycomd` stay.

**Tech Stack:** Go 1.26, Mintlify (`docs.json` + MDX, theme `mint`), GitHub Actions (`mint` CLI + [staticmint](https://github.com/migetapp/staticmint) + `actions/deploy-pages`).

**Spec:** `docs/superpowers/specs/2026-09-12-lazycomd-docs-pages-design.md`

## Global Constraints

- Module path: `github.com/thanhphuchuynh/lazycomd`.
- Docs root: `website/` (not repo-root `docs.json`; `docs/` stays internal specs).
- Live URL: `https://thanhphuchuynh.github.io/lazycomd/`.
- Docs workflow triggers only on tags `v*`. `main` pushes do not deploy.
- `mint export` is not used (Enterprise; needs `node serve.js`).
- Do not rewrite `docs/superpowers/` historical files.
- Do not rename `contrib/com.tphuc.lazycomd.plist` or the launchd label.
- No `replace` directive, no `/v2` module path.
- Do not create the GitHub repo, enable Pages, or `git push`.
- Drop the README “Not in this version” paragraph.

---

## File Structure

| Path | Responsibility |
|---|---|
| `go.mod` | Module path |
| `cmd/**/*.go`, `internal/**/*.go` | Import path rewrite |
| `internal/depsguard/depsguard_test.go` | Daemon dep guard + module-path assertion |
| `website/docs.json` | Mintlify site config and nav |
| `website/index.mdx` … `behavior.mdx` | Split README content |
| `website/nav_test.go` | `docs.json` is valid and every page file exists |
| `.github/workflows/docs.yml` | Tag-only Pages deploy |
| `README.md` | Short landing page + docs URL |

---

### Task 1: Module path

**Files:**
- Modify: `go.mod`
- Modify: every `*.go` under `cmd/` and `internal/` (imports only)
- Modify: `internal/depsguard/depsguard_test.go`

**Interfaces:**
- Consumes: existing packages.
- Produces: module and imports `github.com/thanhphuchuynh/lazycomd/...`.

- [ ] **Step 1: Write the failing module-path test**

Add to `internal/depsguard/depsguard_test.go`:

```go
func TestModulePath(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(b), "\n")
	want := "module github.com/thanhphuchuynh/lazycomd"
	if first != want {
		t.Fatalf("go.mod first line = %q, want %q", first, want)
	}
}
```

Add `"path/filepath"` to imports.

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/depsguard -run TestModulePath -v`

Expected: FAIL, first line is `module github.com/tphuc/lazycomd`.

- [ ] **Step 3: Rewrite module and imports**

```bash
go mod edit -module github.com/thanhphuchuynh/lazycomd
find cmd internal -name '*.go' -print0 | xargs -0 sed -i '' 's|github.com/tphuc/lazycomd|github.com/thanhphuchuynh/lazycomd|g'
```

Do not touch `docs/superpowers/`. Update `daemonPkgs` in `depsguard_test.go` as part of the same rewrite.

- [ ] **Step 4: Tests pass**

Run: `go test ./internal/depsguard -run TestModulePath -v && go test ./...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod cmd internal
git commit -m "refactor: module path github.com/thanhphuchuynh/lazycomd"
```

---

### Task 2: Mintlify source

**Files:**
- Create: `website/docs.json`
- Create: `website/index.mdx`, `website/install.mdx`, `website/tui.mdx`, `website/configuration.mdx`, `website/cli.mdx`, `website/api.mdx`, `website/behavior.mdx`
- Create: `website/nav_test.go`

**Interfaces:**
- Consumes: current `README.md` (split by heading; drop “Not in this version”).
- Produces: Mintlify site whose nav pages are exactly the seven files above.

- [ ] **Step 1: Write the failing nav test**

`website/nav_test.go`:

```go
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
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./website -v`

Expected: FAIL, `docs.json` missing.

- [ ] **Step 3: Add `docs.json` and the seven MDX pages**

`website/docs.json`:

```json
{
  "$schema": "https://mintlify.com/docs.json",
  "theme": "mint",
  "name": "lazycomd",
  "colors": { "primary": "#0F766E" },
  "navbar": {
    "primary": {
      "type": "button",
      "label": "GitHub",
      "href": "https://github.com/thanhphuchuynh/lazycomd"
    }
  },
  "footer": {
    "socials": {
      "github": "https://github.com/thanhphuchuynh/lazycomd"
    }
  },
  "navigation": {
    "groups": [
      { "group": "Get started", "pages": ["index", "install"] },
      { "group": "Use", "pages": ["tui", "cli"] },
      { "group": "Configure", "pages": ["configuration"] },
      { "group": "Reference", "pages": ["api", "behavior"] }
    ]
  }
}
```

MDX files are the README sections, with Mintlify frontmatter (`title`, `description`). `index.mdx` is the opening pitch. `install.mdx` is Install. `tui.mdx` is The TUI including editing and dashboard. `cli.mdx` is Commands. `configuration.mdx` is Configuration plus Environment. `api.mdx` is API. `behavior.mdx` is Behavior worth knowing. Do not copy “Not in this version”. Use module path `github.com/thanhphuchuynh/lazycomd` wherever an import or repo URL appears.

- [ ] **Step 4: Tests pass**

Run: `go test ./website -v && go test ./...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add website
git commit -m "docs: Mintlify source in website/"
```

---

### Task 3: Tag workflow

**Files:**
- Create: `.github/workflows/docs.yml`

**Interfaces:**
- Consumes: `website/` from Task 2.
- Produces: GitHub Pages deploy on `v*` tags only.

- [ ] **Step 1: Add the workflow**

`.github/workflows/docs.yml`:

```yaml
name: docs

on:
  push:
    tags: ["v*"]

permissions:
  contents: read
  pages: write
  id-token: write

concurrency:
  group: pages
  cancel-in-progress: false

jobs:
  deploy:
    runs-on: ubuntu-latest
    environment:
      name: github-pages
      url: ${{ steps.deployment.outputs.page_url }}
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-node@v4
        with:
          node-version: "20"

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Install mint and staticmint
        run: |
          npm i -g mint
          go install github.com/migetapp/staticmint@latest
          echo "$(go env GOPATH)/bin" >> "$GITHUB_PATH"

      - name: Export static site
        run: staticmint -start-server -docs website -out _site -site-url https://thanhphuchuynh.github.io/lazycomd

      - uses: actions/configure-pages@v5

      - uses: actions/upload-pages-artifact@v3
        with:
          path: _site

      - id: deployment
        uses: actions/deploy-pages@v4
```

A failed mint/staticmint step fails the job. There is no `continue-on-error` and no deploy of a missing `_site`.

- [ ] **Step 2: Confirm trigger is tags only**

`grep -n "tags\|branches\|pull_request" .github/workflows/docs.yml` shows `tags: ["v*"]` and no `branches:` / `pull_request`.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/docs.yml
git commit -m "ci: deploy Mintlify docs to GitHub Pages on v* tags"
```

---

### Task 4: Short README

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: live docs URL from the spec.
- Produces: GitHub landing page that does not duplicate the site tables.

- [ ] **Step 1: Replace README with the short landing page**

```markdown
# lazycomd

One user-wide daemon that runs, supervises and exposes your long dev
commands — proxies, tunnels, local service stacks — over an HTTP API.

Docs: https://thanhphuchuynh.github.io/lazycomd/

## Install

```bash
go build -o ~/.local/bin/lazycomd ./cmd/lazycomd
```

Run the daemon in the foreground, or install one of the unit files in
`contrib/`:

```bash
lazycomd serve
```

With no arguments, `lazycomd` opens the TUI (a TTY is required). Quitting
the TUI does not stop the daemon.

```

No “Not in this version”. Module/repo is `thanhphuchuynh/lazycomd` if mentioned.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: point README at the GitHub Pages site"
```
