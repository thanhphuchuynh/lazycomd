# lazycomd — Config Writes Design (Spec #4)

Date: 2026-09-11
Status: Approved for planning
Scope: Adding, editing and removing commands from the TUI and the API, with
the daemon writing the config file.

## Problem

Every command lazycomd knows comes from a YAML file edited by hand. Adding a
proxy means leaving the tool, finding `~/.config/lazycomd/config.yaml`,
remembering the schema, and coming back. Fixing a typo in a command you just
added means the same trip. Removing one that has outlived its usefulness means
a third.

The TUI already shows every command and drives its lifecycle. It should be
able to add one, fix one, and remove one — and the same operations should be
available over the API, so this is scriptable and works the same way from a
client.

## Relationship to the other specs

- Spec #1 (shipped) — daemon, process manager, HTTP API, CLI.
- Spec #2 (shipped) — the TUI, since restructured into lazygit-style panels.
- Spec #3 (shipped) — dashboard signals: ports, vitals, health, conflicts.
- **Spec #4 (this document) — writing the config.**

This is the first feature where the daemon modifies a file the user owns and
may have committed to git. That constraint shapes every decision below.

## Approach

A new `internal/configw` package owns file modification. `internal/config`
stays read-only: parse, validate, resolve. Three new endpoints expose create,
update and delete; the TUI gets a form popup on `a`, the same form prefilled
on `e`, and a confirmed delete on `d`.

Two alternatives were rejected:

- **Put the writer in `internal/config`.** One fewer package, but that file
  would become both the schema's reader and its rewriter, and file locking and
  modification-time concerns would land in the package every other part of the
  daemon depends on.
- **Put the writer in `internal/api`.** No new package at all, at the cost of
  YAML line arithmetic living behind an HTTP handler, which is exactly the
  kind of code that ends up untested.

A fourth shape was considered and dropped early: having `e` shell out to
`$EDITOR` the way lazygit does for commit messages. It is much less code and
it keeps the file's ownership unambiguous, but it does not give the API the
same abilities, and the request was explicitly for a popup plus an API.

## The writer

```go
package configw

// File is one config file this process may modify. Writes are serialized.
type File struct {
    path string
    mu   sync.Mutex
}

func Open(path string) *File
func (f *File) Create(name string, c config.Command) error
func (f *File) Update(name string, c config.Command) error
func (f *File) Delete(name string) error

// ErrChanged means the file moved under us between read and write.
var ErrChanged = errors.New("config changed on disk")
```

Every operation runs the same five steps:

1. Read the file; note its size and modification time.
2. Parse it into a `yaml.Node` tree.
3. Locate the target command's line range.
4. Splice the new text into those lines.
5. Re-parse and validate the result, then write it.

**Validation happens before the write, not after.** A splice that would
produce a file the daemon cannot load is refused and nothing touches disk. The
daemon can never render itself unable to start through its own edit.

### Finding a command's lines

`yaml.Node` carries a `Line` for every key, so a command's block runs from its
key line to the line before the next key at the same indentation, or to the
end of the `commands` mapping for the last one.

Deleting scans backwards from that end and leaves trailing comments and blank
lines in place: a comment sitting directly above the next command belongs to
that command, and removing it along with its neighbour would be a quiet
mistake in a file someone else reads.

### Rendering a block

New YAML comes from marshalling a small struct whose optional fields are all
`omitempty`, so a command with a `cmd` and a `restart` emits two lines rather
than the whole schema padded out with zeros. The output is re-indented to
match the file's existing `commands:` indentation.

### Writing safely

Temp file in the same directory, `fsync`, `rename`. A crash mid-write leaves
the previous file intact rather than half of a new one. The original file mode
is preserved.

Immediately before the rename the file is re-stat'd. If its size or
modification time moved since step 1, the write is abandoned with
`ErrChanged`. With the config open in `$EDITOR`, the human wins and the API
says so, instead of silently overwriting what was being typed.

## API

```
GET    /v1/commands/{name}/config                                     → 200 + config.Command
POST   /v1/commands          {"name":"web","cmd":["npm","start"],...} → 201 + status
PUT    /v1/commands/{name}   the same body without name               → 200 + status
DELETE /v1/commands/{name}                                            → 204
```

`PUT` **replaces** a command's whole spec, which makes the read endpoint part
of the feature rather than a convenience: a form that knows four fields must
not be able to erase the `env`, `depends_on`, `health` and `port` of a command
that has them. So `e` first fetches `GET /v1/commands/{name}/config`, overwrites
the four fields it owns, and sends the rest back untouched.

The alternative — merge semantics, where an absent field means "leave it" —
needs a pointer for every field to tell absent from zero, and makes clearing a
value impossible to express. Replace plus a read is the smaller, clearer
contract.

The body is a `config.Command` plus a `name`, so whatever the schema accepts,
the API accepts. The TUI's four fields are a client-side choice, not a
protocol limit.

### Which file a write targets

A bare name resolves to the global config. A namespaced name like `app:api`
resolves to that project's `lazycomd.yaml`, and the key written inside that
file is the bare `api`, because project files hold unprefixed names.

For this, `config.Load` stops discarding what it already computes: `Config`
gains `Projects map[string]string`, mapping a project's basename to its
directory. An unknown namespace is a 400 that names the projects that do
exist.

### Applying the change

After a successful write the daemon runs the path `POST /v1/reload` already
uses: `config.Load`, then `mgr.Reload`. Nothing new is needed for the result
to take effect. A created command appears as `stopped`; an edited one that is
running gets the existing `spec_dirty` treatment and picks the change up on
its next start; a deleted one is stopped and dropped, so delete needs no
special case for a running process.

### Status codes

| Code | When |
|---|---|
| 400 | invalid body, failed validation, unknown project namespace |
| 404 | `PUT` or `DELETE` for a command the config does not have |
| 409 | `POST` for a name already taken, or `ErrChanged` |
| 500 | the write itself failed |

An `ErrChanged` 409 names the file, so the message is actionable:
`config.yaml changed on disk since it was read — reload and try again`.

### Client

`Create(name string, c config.Command) (manager.Status, error)`, `Update` with
the same shape, and `Delete(name string) error`. That also makes these
operations available to the CLI later without touching the API again.

## The form

`a` opens it empty. `e` opens it filled from the selected command. `d` deletes
after a confirmation. All three act on the Commands panel; pressed elsewhere
they say to press `2` first, matching how the lifecycle keys already behave.

```
╭─ New command ──────────────────────────────────╮
│                                                │
│  NAME     web                                  │
│  COMMAND  npm start                            │
│  FOLDER   ~/coding/app                         │
│  RESTART  ‹ on-failure ›                       │
│                                                │
│  where the command runs · blank = ~            │
│  tab next · enter save · esc cancel            │
╰────────────────────────────────────────────────╯
```

`tab` and `shift+tab` move between fields, `enter` saves from anywhere, `esc`
cancels. `RESTART` cycles `no`, `on-failure`, `always` with `←`/`→` or
`space`: three fixed choices do not deserve free text.

The hint line follows focus — `app:name puts it in that project` on NAME,
`split on spaces · pipes and $ switch to shell mode` on COMMAND, the folder
hint above on FOLDER, and the three values on RESTART.

### FOLDER is labelled, not abbreviated, and prefilled

The field is `FOLDER`, not `CWD`. The YAML key stays `cwd`, which is the
standard name and what other people and tools expect to read in the file, but
the form says the plain word and explains it in the hint.

It opens prefilled with the directory the TUI was launched from: you add a
command for the project you are sitting in, so that guess is usually right.
The prefill is skipped when the client is pointed at a TCP address, because a
path from this machine means nothing on the daemon's — there the field opens
blank.
Typing a namespaced name switches the prefill to that project's registered
directory, because that is where the command is about to live. Both are
ordinary text that can be overwritten, and clearing the field means what a
blank `cwd:` means in the file — the home directory.

### The command line becomes `cmd`

The line splits on whitespace into the string array spec #1 chose
deliberately. If it contains a shell metacharacter — `|`, `>`, `<`, `&`, `;`,
`$` or `*` — the form sets `shell: true` and passes the line through whole,
because `while true; do ...; done` split on spaces is nonsense. The form shows
which reading it used, so the choice is never silent.

### Editing does not rename

On `e` the name field is read-only. Renaming is a delete plus a create,
possibly across two files, and doing that quietly from a form is worse than
declining. Rename stays a file edit.

### Removing a command

`d` on the selected command asks first:

```
delete web from config.yaml? (y/n)
```

The prompt names the file that will actually change, so deleting `app:api`
reads `delete app:api from app/lazycomd.yaml?` — a project file being edited
should never be a surprise.

`y` sends the `DELETE`, which removes the block from the file and — through
the ordinary reload — stops the command if it was running. `n` or `esc` does
nothing. The confirmation exists because, unlike stopping a process, this
edits a file that may be committed.

### Errors stay in the form

A 400 or 409 renders on the warning line with every field's input intact, so a
duplicate name or a malformed health URL is one keystroke from fixed rather
than retyped from scratch.

On a successful create the form closes and the new command is selected in the
table, so `s` starts it immediately.

## Testing

| Unit | Cases |
|---|---|
| `configw` | create into a file with comments, with every untouched line byte-identical; create into a file with no `commands:` key; update leaves neighbouring commands alone; delete keeps the comment belonging to the next command; delete the last command in the file; a write that would fail validation is refused and the file is unchanged; `ErrChanged` when the modification time moves between read and rename; the file mode is preserved; a failed write leaves the original intact |
| `config` | `Projects` is populated with basename to directory |
| `api` | 201, 200 and 204 happy paths against a temp config; 409 on a duplicate name; 409 on `ErrChanged`; 400 on an unknown namespace; 404 on `PUT` and `DELETE` of an unknown name; a namespaced create lands in the project file under its bare key; the command appears in `GET /v1/commands` afterwards; `GET /v1/commands/{name}/config` returns every field, including ones the form never shows |
| `client` | `Create`, `Update` and `Delete` round trip; `CommandConfig` returns the full spec |
| `tui` | an edit of a command carrying `env` and `health` preserves both; the delete prompt names the project file for a namespaced command; field navigation; restart cycles through all three values; a metacharacter switches to shell mode and the form says so; a blank folder; FOLDER prefilled from the launch directory, and re-prefilled when the name gains a namespace; an API error renders in the form with input kept; `esc` cancels; `e` prefills and locks the name; `d` requires a `y` |
| integration | a real daemon over a temp config: the form's path writes the file, the daemon lists the command, and it is still there after a restart |

## Non-goals

Renaming; editing `env`, `depends_on`, `health` or `port` from the form;
editing the `projects:` list; moving a command between files; undo. All of
those stay file edits.

## Definition of done

- `a` adds a command that is still there after `lazycomd serve` restarts.
- `git diff` on a project config shows only the added block, with comments and
  formatting elsewhere untouched.
- `e` fixes a typo in a command without leaving the TUI, and a command with
  `env` or `health` set still has them afterwards.
- `d` removes a command after a confirmation, stopping it if it was running.
- With the config open in an editor, a write loses and says which file moved.
- `go test -race ./...` passes.
- `README.md` documents the three keys and the three endpoints.
