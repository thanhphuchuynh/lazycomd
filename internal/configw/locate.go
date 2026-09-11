package configw

import "gopkg.in/yaml.v3"

// commandsNode returns the mapping node under the top-level "commands" key.
func commandsNode(doc *yaml.Node) (*yaml.Node, bool) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, false
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, false
	}
	// A mapping's Content alternates key, value, key, value.
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "commands" && root.Content[i+1].Kind == yaml.MappingNode {
			return root.Content[i+1], true
		}
	}
	return nil, false
}

// blockRange reports the 1-based inclusive line range one command occupies and
// the indentation of its key.
//
// The end is the deepest line of the command's own value, never the line
// before the next key: a comment sitting between two commands belongs to the
// one below it, and including it here would delete somebody else's note.
func blockRange(doc *yaml.Node, name string) (int, int, int, bool) {
	cmds, ok := commandsNode(doc)
	if !ok {
		return 0, 0, 0, false
	}
	for i := 0; i+1 < len(cmds.Content); i += 2 {
		key, val := cmds.Content[i], cmds.Content[i+1]
		if key.Value != name {
			continue
		}
		return key.Line, maxLine(val), key.Column - 1, true
	}
	return 0, 0, 0, false
}

// commandsEnd is the last line of the commands mapping and the indentation its
// keys use, for appending a new command.
func commandsEnd(doc *yaml.Node) (int, int, bool) {
	cmds, ok := commandsNode(doc)
	if !ok {
		return 0, 0, false
	}
	if len(cmds.Content) == 0 {
		// "commands: {}" or an empty mapping: append just below it.
		return cmds.Line, cmds.Column - 1, true
	}
	last := cmds.Content[len(cmds.Content)-1]
	return maxLine(last), cmds.Content[0].Column - 1, true
}

// maxLine is the deepest line any part of a node reaches.
//
// ponytail: a multi-line block scalar reports its start line, so a command
// written with `cmd: |` would under-report its end. lazycomd commands are one
// line each; revisit if that ever stops being true.
func maxLine(n *yaml.Node) int {
	line := n.Line
	for _, c := range n.Content {
		if l := maxLine(c); l > line {
			line = l
		}
	}
	return line
}
