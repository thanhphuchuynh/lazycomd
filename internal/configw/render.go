// Package configw modifies lazycomd config files in place, changing only the
// lines a command occupies so the rest of the file — comments, quoting,
// blank lines — survives byte-for-byte.
package configw

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tphuc/lazycomd/internal/config"
)

// wire mirrors config.Command with every optional field omitempty, so a
// command with two fields set renders as two lines rather than the whole
// schema padded out with zeros.
type wire struct {
	Cmd       []string          `yaml:"cmd"`
	Cwd       string            `yaml:"cwd,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	Shell     bool              `yaml:"shell,omitempty"`
	Restart   string            `yaml:"restart,omitempty"`
	Autostart bool              `yaml:"autostart,omitempty"`
	Log       bool              `yaml:"log,omitempty"`
	Size      int               `yaml:"size,omitempty"`
	DependsOn []string          `yaml:"depends_on,omitempty"`
	Health    string            `yaml:"health,omitempty"`
	Port      int               `yaml:"port,omitempty"`
}

// renderBlock returns the YAML lines for one command, with its key indented by
// indent spaces and its fields one level further in.
func renderBlock(name string, c config.Command, indent int) ([]string, error) {
	w := wire{
		Cmd:       c.Cmd,
		Cwd:       c.Cwd,
		Env:       c.Env,
		Shell:     c.Shell,
		Autostart: c.Autostart,
		Log:       c.Log,
		Size:      c.Size,
		DependsOn: c.DependsOn,
		Health:    c.Health,
		Port:      c.Port,
	}
	// "no" is the default; writing it only adds noise.
	if c.Restart != "" && c.Restart != config.RestartNo {
		w.Restart = string(c.Restart)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]wire{name: w}); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	pad := strings.Repeat(" ", indent)
	var out []string
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		out = append(out, pad+line)
	}
	return out, nil
}
