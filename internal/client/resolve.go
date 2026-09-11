package client

import (
	"fmt"
	"sort"
	"strings"
)

// Resolve turns a bare name into a qualified command name: an exact match
// wins, then a unique project-qualified match. An ambiguous name is an error
// listing every candidate.
func (c *Client) Resolve(name string) (string, error) {
	list, err := c.List()
	if err != nil {
		return "", err
	}
	var matches []string
	for _, s := range list {
		if s.Name == name {
			return name, nil
		}
		if strings.HasSuffix(s.Name, ":"+name) {
			matches = append(matches, s.Name)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("unknown command %q", name)
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("ambiguous command %q: %s", name, strings.Join(matches, ", "))
	}
}
