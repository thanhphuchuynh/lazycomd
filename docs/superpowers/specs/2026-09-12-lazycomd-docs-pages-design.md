# lazycomd — Mintlify docs on GitHub Pages, module rename

Date: 2026-09-12
Status: Approved for planning
Scope: Mintlify documentation source, tag-triggered GitHub Pages deploy
(latest only), and Go module path rename to `github.com/thanhphuchuynh/lazycomd`.

## Problem

The project is documented only in `README.md`. That file is already the
product description, but it is not a docs site, it is not versioned with
releases, and it still claims the TUI is “not in this version.” The Go
module path is `github.com/tphuc/lazycomd`; the public repo will be
`github.com/thanhphuchuynh/lazycomd`. There is no CI.

## Approach

Mintlify source in-repo. On each `v*` tag, CI builds a static snapshot and
publishes it to GitHub Pages, overwriting the previous site. The frozen
copy of an older release is the git tag, not a second URL.

Three alternatives were considered and rejected:

- **Versioned URLs (`/v0.1.0/` plus latest).** Extra crawler path and
  `keep_files` complexity. Rejected in favour of latest-only Pages.
- **`mint export` into Pages.** Enterprise-only, and the zip is served with
  `node serve.js`, which GitHub Pages cannot run.
- **Mintlify GitHub App on a `release` branch.** Needs a Mintlify account
  and their app; not GitHub Pages.

`mint export` is unusable here, so the tag job starts `mint dev` and crawls
it to static HTML with [staticmint](https://github.com/migetapp/staticmint),
then deploys that tree.

## Repository layout

```
website/
  docs.json
  index.mdx
  install.mdx
  tui.mdx
  configuration.mdx
  cli.mdx
  api.mdx
  behavior.mdx
.github/workflows/docs.yml
```

`website/` is the Mintlify root so `docs/` can keep internal superpowers
specs and mockups. `docs.json` is not placed at the repo root.

## Site

`docs.json`: theme `mint`, name `lazycomd`, GitHub
`https://github.com/thanhphuchuynh/lazycomd`. Navbar primary button is the
repo. Footer GitHub is the same URL.

Navigation is the current README, split so each item is one page. No new
product behaviour. Drop the stale “Not in this version” paragraph.

| Group | Page | Content |
|---|---|---|
| Get started | `index.mdx` | What the daemon is |
| Get started | `install.mdx` | `go build`, `serve`, contrib units |
| Use | `tui.mdx` | Layout, keys, mouse, config form, dashboard |
| Use | `cli.mdx` | Commands, name resolution, exit codes |
| Configure | `configuration.mdx` | YAML, projects, env vars |
| Reference | `api.mdx` | Socket/token, routes, status object |
| Reference | `behavior.mdx` | Reload, deps, process groups, logs, ports |

`README.md` becomes a short GitHub landing page: one-paragraph what, the
install one-liner, and a link to
`https://thanhphuchuynh.github.io/lazycomd/`. Full tables live on the site
only.

Local preview: from `website/`, `mint dev` (or `npx mint dev`).

## Deploy

Workflow `.github/workflows/docs.yml` runs only on tags matching `v*`.
Pushes to `main` do not deploy docs.

On a matching tag:

1. Check out the tagged commit.
2. Install Node 20 and the Mintlify CLI (`mint`).
3. Install staticmint. Crawl `mint dev` in `website/` to static HTML, with
   site URL `https://thanhphuchuynh.github.io/lazycomd/` so asset paths
   match a project Pages site (`/lazycomd/` base).
4. Upload the output as a GitHub Pages artifact and deploy.

A failed build fails the job. It must not publish a partial tree.

Live URL: `https://thanhphuchuynh.github.io/lazycomd/`. Each new tag
overwrites that URL.

One-time GitHub settings (human, not this change): create
`thanhphuchuynh/lazycomd`, enable Pages with source **GitHub Actions**.
This workspace has no push of that repo as part of the implementation.

## Module path

`github.com/tphuc/lazycomd` becomes
`github.com/thanhphuchuynh/lazycomd`.

Change: `go.mod`, every Go import in `cmd/` and `internal/` (including
tests), `internal/depsguard` package names, README and `website/` module
paths.

Leave: `docs/superpowers/` historical plans and specs; launchd label
`com.tphuc.lazycomd` and `contrib/com.tphuc.lazycomd.plist` filename. No
`replace` directive and no `/v2` path — this is a pre-release rename.

After the code change, origin should be
`https://github.com/thanhphuchuynh/lazycomd.git`. Creating the GitHub
repo, enabling Pages, and pushing are out of band.

## Checks

- `go test ./...` after the import rewrite.
- `website/docs.json` is valid JSON; every page it names exists.
- Docs workflow triggers only on `v*` tags.

## Out of scope

- GoReleaser or binary release artifacts
- Per-version Pages URLs
- Mintlify GitHub App / `*.mintlify.app` hosting
- Rewriting historical superpowers documents
- Creating the GitHub repository, enabling Pages, or pushing
