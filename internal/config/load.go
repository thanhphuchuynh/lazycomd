package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is the merged, validated configuration the daemon runs from.
// Commands is keyed by "name" for global commands and "project:name" for
// project commands.
type Config struct {
	Listen    string
	TokenFile string
	Commands  map[string]Command
	Projects  map[string]string // project basename -> directory
}

// Load reads the global config plus every registered project file, merges
// them, resolves dependency names and rejects dependency cycles.
func Load(globalPath string) (*Config, error) {
	gf, err := ParseFile(globalPath)
	if err != nil {
		return nil, err
	}
	if err := gf.Validate(); err != nil {
		return nil, err
	}

	cfg := &Config{
		Listen:    gf.Listen,
		TokenFile: gf.TokenFile,
		Commands:  make(map[string]Command, len(gf.Commands)),
	}
	for n, c := range gf.Commands {
		cfg.Commands[n] = c
	}

	byBase := make(map[string]string, len(gf.Projects))
	for _, raw := range gf.Projects {
		dir := ExpandUser(raw)
		ns := filepath.Base(dir)
		if prev, ok := byBase[ns]; ok {
			return nil, fmt.Errorf("project basename %q is used by both %s and %s", ns, prev, dir)
		}
		byBase[ns] = dir

		path := filepath.Join(dir, "lazycomd.yaml")
		pf, err := ParseFile(path)
		if err != nil {
			return nil, err
		}
		if pf.Listen != "" || pf.TokenFile != "" || len(pf.Projects) > 0 {
			return nil, fmt.Errorf("%s: listen, token_file and projects are global-only", path)
		}
		if err := pf.Validate(); err != nil {
			return nil, err
		}
		for n, c := range pf.Commands {
			if c.Cwd == "" {
				c.Cwd = dir
			}
			cfg.Commands[ns+":"+n] = c
		}
	}

	cfg.Projects = byBase

	if err := cfg.resolveDeps(); err != nil {
		return nil, err
	}
	return cfg, cfg.checkCycles()
}

// ExpandUser replaces a leading ~ with the user's home directory.
func ExpandUser(s string) string {
	if s != "~" && !strings.HasPrefix(s, "~/") {
		return s
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	return filepath.Join(h, strings.TrimPrefix(strings.TrimPrefix(s, "~"), "/"))
}

// resolveDeps rewrites every depends_on entry to a fully qualified key.
func (c *Config) resolveDeps() error {
	for _, name := range c.sortedNames() {
		cmd := c.Commands[name]
		ns := ""
		if i := strings.Index(name, ":"); i >= 0 {
			ns = name[:i+1]
		}
		out := make([]string, 0, len(cmd.DependsOn))
		for _, d := range cmd.DependsOn {
			resolved, ok := c.resolveOne(ns, d)
			if !ok {
				return fmt.Errorf("command %q: unknown dependency %q", name, d)
			}
			out = append(out, resolved)
		}
		cmd.DependsOn = out
		c.Commands[name] = cmd
	}
	return nil
}

func (c *Config) resolveOne(ns, dep string) (string, bool) {
	if strings.Contains(dep, ":") {
		_, ok := c.Commands[dep]
		return dep, ok
	}
	if ns != "" {
		if _, ok := c.Commands[ns+dep]; ok {
			return ns + dep, true
		}
	}
	_, ok := c.Commands[dep]
	return dep, ok
}

func (c *Config) checkCycles() error {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make(map[string]int, len(c.Commands))
	var path []string

	var visit func(string) error
	visit = func(n string) error {
		switch color[n] {
		case grey:
			return fmt.Errorf("dependency cycle: %s -> %s", strings.Join(path, " -> "), n)
		case black:
			return nil
		}
		color[n] = grey
		path = append(path, n)
		for _, d := range c.Commands[n].DependsOn {
			if err := visit(d); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		color[n] = black
		return nil
	}

	for _, n := range c.sortedNames() {
		if err := visit(n); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) sortedNames() []string {
	out := make([]string, 0, len(c.Commands))
	for n := range c.Commands {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
