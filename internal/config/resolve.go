package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Resolved is a Command with ~ and $VAR expanded, ready to spawn.
type Resolved struct {
	Argv []string
	Cwd  string
	Env  []string
}

// Resolve expands the command for the current environment. It runs at start
// time, not config load time, so a restart picks up environment changes.
func (c Command) Resolve() (Resolved, error) {
	if len(c.Cmd) == 0 {
		return Resolved{}, errors.New("cmd is empty")
	}
	// os.ExpandEnv eats shell constructs like $! and $1, so $$ is the escape
	// for a literal dollar sign.
	expand := func(s string) string {
		const sentinel = "\x00"
		s = strings.ReplaceAll(s, "$$", sentinel)
		s = os.ExpandEnv(ExpandUser(s))
		return strings.ReplaceAll(s, sentinel, "$")
	}

	argv := make([]string, 0, len(c.Cmd))
	for _, a := range c.Cmd {
		argv = append(argv, expand(a))
	}
	if c.Shell {
		argv = []string{"sh", "-c", strings.Join(argv, " ")}
	}

	cwd := expand(c.Cwd)
	if cwd == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Resolved{}, fmt.Errorf("cwd unset and home unknown: %w", err)
		}
		cwd = h
	}
	if fi, err := os.Stat(cwd); err != nil {
		return Resolved{}, fmt.Errorf("cwd %s: %w", cwd, err)
	} else if !fi.IsDir() {
		return Resolved{}, fmt.Errorf("cwd %s: not a directory", cwd)
	}

	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	env := os.Environ()
	for _, k := range keys {
		env = append(env, k+"="+expand(c.Env[k]))
	}
	return Resolved{Argv: argv, Cwd: cwd, Env: env}, nil
}
