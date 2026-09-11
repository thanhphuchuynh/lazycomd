// Package config loads and validates lazycomd's YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

// DefaultBufSize is the per-command log ring buffer size when size: is unset.
const DefaultBufSize = 256 * 1024

// Restart is a command's restart policy.
type Restart string

const (
	RestartNo        Restart = "no"
	RestartOnFailure Restart = "on-failure"
	RestartAlways    Restart = "always"
)

// Command is one configured command.
type Command struct {
	Cmd       []string          `yaml:"cmd" json:"cmd"`
	Cwd       string            `yaml:"cwd" json:"cwd,omitempty"`
	Env       map[string]string `yaml:"env" json:"env,omitempty"`
	Shell     bool              `yaml:"shell" json:"shell,omitempty"`
	Restart   Restart           `yaml:"restart" json:"restart,omitempty"`
	Autostart bool              `yaml:"autostart" json:"autostart,omitempty"`
	Log       bool              `yaml:"log" json:"log,omitempty"`
	Size      int               `yaml:"size" json:"size,omitempty"`
	DependsOn []string          `yaml:"depends_on" json:"depends_on,omitempty"`
	Health    string            `yaml:"health" json:"health,omitempty"`
	Port      int               `yaml:"port" json:"port,omitempty"`
}

// File is one YAML file on disk. Listen, TokenFile and Projects are
// meaningful in the global config only.
type File struct {
	Listen    string             `yaml:"listen"`
	TokenFile string             `yaml:"token_file"`
	Projects  []string           `yaml:"projects"`
	Commands  map[string]Command `yaml:"commands"`
}

// ParseFile decodes one YAML file, rejecting unknown fields.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data, path)
}

// ParseBytes decodes YAML that is already in memory. name appears in errors,
// so a spliced buffer can be validated before it is written anywhere.
func ParseBytes(data []byte, name string) (*File, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var out File
	if err := dec.Decode(&out); err != nil {
		if errors.Is(err, io.EOF) {
			return &File{Commands: map[string]Command{}}, nil
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if out.Commands == nil {
		out.Commands = map[string]Command{}
	}
	return &out, nil
}

// Validate checks every command and applies defaults in place.
func (f *File) Validate() error {
	names := make([]string, 0, len(f.Commands))
	for n := range f.Commands {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		c := f.Commands[n]
		if err := c.validate(n); err != nil {
			return err
		}
		f.Commands[n] = c
	}
	if f.Listen != "" && f.TokenFile == "" {
		return errors.New("listen requires token_file")
	}
	return nil
}

func (c *Command) validate(name string) error {
	if len(c.Cmd) == 0 {
		return fmt.Errorf("command %q: cmd is empty", name)
	}
	switch c.Restart {
	case "":
		c.Restart = RestartNo
	case RestartNo, RestartOnFailure, RestartAlways:
	default:
		return fmt.Errorf("command %q: invalid restart %q (want no, on-failure or always)", name, c.Restart)
	}
	if c.Size < 0 {
		return fmt.Errorf("command %q: size must be >= 0", name)
	}
	if c.Size == 0 {
		c.Size = DefaultBufSize
	}
	if c.Health != "" {
		u, err := url.Parse(c.Health)
		if err != nil {
			return fmt.Errorf("command %q: health %q: %w", name, c.Health, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("command %q: health %q: scheme must be http or https", name, c.Health)
		}
		if u.Host == "" {
			return fmt.Errorf("command %q: health %q: missing host", name, c.Health)
		}
	}
	if c.Port != 0 && (c.Port < 1 || c.Port > 65535) {
		return fmt.Errorf("command %q: port %d out of range 1-65535", name, c.Port)
	}
	return nil
}

// IntendedPort is the port this command means to bind: the explicit port:
// field, otherwise the port in its health URL, otherwise 0. Used to tell a
// command's own listener apart from something else squatting on its port.
func (c Command) IntendedPort() int {
	if c.Port != 0 {
		return c.Port
	}
	if c.Health == "" {
		return 0
	}
	u, err := url.Parse(c.Health)
	if err != nil {
		return 0
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0
		}
		return n
	}
	switch u.Scheme {
	case "http":
		return 80
	case "https":
		return 443
	}
	return 0
}
