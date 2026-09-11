package configw

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/tphuc/lazycomd/internal/config"
)

var (
	ErrExists   = errors.New("command already exists")
	ErrNotFound = errors.New("command not found")
	ErrInvalid  = errors.New("refusing to write")
)

// File is one config file this process may modify. Its mutex serializes the
// read-modify-write cycle, so two clients cannot interleave into one file.
type File struct {
	path string
	mu   sync.Mutex
}

// Registry hands out one File per path, so the same file always shares a
// mutex no matter who asks for it.
type Registry struct {
	mu    sync.Mutex
	files map[string]*File
}

func NewRegistry() *Registry {
	return &Registry{files: make(map[string]*File)}
}

func (r *Registry) File(path string) *File {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.files[path]; ok {
		return f
	}
	f := &File{path: path}
	r.files[path] = f
	return f
}

// Create adds a command, refusing a name the file already has.
func (f *File) Create(name string, c config.Command) error {
	return f.edit(func(lines []string, doc *yaml.Node) ([]string, error) {
		if _, _, _, exists := blockRange(doc, name); exists {
			return nil, fmt.Errorf("%w: %s", ErrExists, name)
		}

		key, cmds, ok := commandsPair(doc)
		if !ok {
			// No commands: key at all; start one at the end of the file.
			block, err := renderBlock(name, c, 2)
			if err != nil {
				return nil, err
			}
			out := trimTrailingBlank(lines)
			out = append(out, "commands:")
			return append(out, block...), nil
		}

		if len(cmds.Content) == 0 {
			// "commands: {}" is a flow mapping, and block entries cannot be
			// spliced under one. Replace the whole line with a block mapping.
			indent := key.Column - 1
			block, err := renderBlock(name, c, indent+2)
			if err != nil {
				return nil, err
			}
			head := strings.Repeat(" ", indent) + "commands:"
			return replaceRange(lines, key.Line, maxLine(cmds), append([]string{head}, block...)), nil
		}

		end, indent, _ := commandsEnd(doc)
		block, err := renderBlock(name, c, indent)
		if err != nil {
			return nil, err
		}
		return insertAt(lines, end, block), nil
	})
}

// Update replaces one command's block, leaving everything around it alone.
func (f *File) Update(name string, c config.Command) error {
	return f.edit(func(lines []string, doc *yaml.Node) ([]string, error) {
		start, end, indent, ok := blockRange(doc, name)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		block, err := renderBlock(name, c, indent)
		if err != nil {
			return nil, err
		}
		return replaceRange(lines, start, end, block), nil
	})
}

// Delete removes one command's block.
func (f *File) Delete(name string) error {
	return f.edit(func(lines []string, doc *yaml.Node) ([]string, error) {
		start, end, _, ok := blockRange(doc, name)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return replaceRange(lines, start, end, nil), nil
	})
}

// edit runs one read-modify-write cycle. The mutated buffer is parsed and
// validated before anything reaches disk, so a bad edit cannot leave the
// daemon unable to load its own config.
func (f *File) edit(mutate func([]string, *yaml.Node) ([]string, error)) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	snap, err := read(f.path)
	if err != nil {
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(snap.data, &doc); err != nil {
		return fmt.Errorf("%s: %w", f.path, err)
	}

	out, err := mutate(strings.Split(string(snap.data), "\n"), &doc)
	if err != nil {
		return err
	}
	blob := []byte(strings.Join(out, "\n"))

	parsed, err := config.ParseBytes(blob, f.path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := parsed.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return writeAtomic(f.path, blob, snap)
}

// insertAt puts block after the given 1-based line.
func insertAt(lines []string, afterLine int, block []string) []string {
	if afterLine < 0 {
		afterLine = 0
	}
	if afterLine > len(lines) {
		afterLine = len(lines)
	}
	out := make([]string, 0, len(lines)+len(block))
	out = append(out, lines[:afterLine]...)
	out = append(out, block...)
	return append(out, lines[afterLine:]...)
}

// replaceRange swaps the 1-based inclusive line range for block, which may be
// empty to delete it.
func replaceRange(lines []string, start, end int, block []string) []string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	out := make([]string, 0, len(lines))
	out = append(out, lines[:start-1]...)
	out = append(out, block...)
	return append(out, lines[end:]...)
}

// trimTrailingBlank drops trailing empty lines, so appending does not leave a
// gap in the middle of the file.
func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
