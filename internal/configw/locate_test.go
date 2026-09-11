package configw

import (
	"testing"

	"gopkg.in/yaml.v3"
)

const sample = `# lazycomd config
listen: ""

commands:
  # the http server
  web:
    cmd: ["npm", "start"]
    cwd: /tmp

  # a chatty one
  noisy:
    cmd: ["sh", "-c", "while true; do echo hi; sleep 1; done"]
    restart: always
`

func parse(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	return &doc
}

func TestBlockRangeFindsACommand(t *testing.T) {
	doc := parse(t, sample)

	start, end, indent, ok := blockRange(doc, "web")
	if !ok {
		t.Fatal("web not found")
	}
	if start != 6 || end != 8 {
		t.Fatalf("range = %d..%d, want 6..8", start, end)
	}
	if indent != 2 {
		t.Fatalf("indent = %d, want 2", indent)
	}
}

func TestBlockRangeLastCommand(t *testing.T) {
	doc := parse(t, sample)

	start, end, _, ok := blockRange(doc, "noisy")
	if !ok {
		t.Fatal("noisy not found")
	}
	if start != 11 {
		t.Fatalf("start = %d, want 11", start)
	}
	if end != 13 {
		t.Fatalf("end = %d, want 13 (its last field, not the end of the file)", end)
	}
}

func TestBlockRangeMissing(t *testing.T) {
	if _, _, _, ok := blockRange(parse(t, sample), "ghost"); ok {
		t.Fatal("ok = true for a command that is not there")
	}
}

func TestBlockRangeExcludesTheFollowingComment(t *testing.T) {
	_, end, _, _ := blockRange(parse(t, sample), "web")
	if end >= 10 {
		t.Fatalf("web's range ends at %d, which swallows the comment on line 10", end)
	}
}

func TestCommandsEnd(t *testing.T) {
	line, indent, ok := commandsEnd(parse(t, sample))
	if !ok {
		t.Fatal("no commands mapping found")
	}
	if line != 13 {
		t.Fatalf("end line = %d, want 13", line)
	}
	if indent != 2 {
		t.Fatalf("indent = %d, want 2", indent)
	}
}

func TestCommandsNodeMissing(t *testing.T) {
	if _, ok := commandsNode(parse(t, "listen: \"\"\n")); ok {
		t.Fatal("ok = true for a file with no commands key")
	}
	if _, _, ok := commandsEnd(parse(t, "listen: \"\"\n")); ok {
		t.Fatal("commandsEnd ok = true for a file with no commands key")
	}
}

func TestBlockRangeFourSpaceIndent(t *testing.T) {
	src := "commands:\n    web:\n        cmd: [\"x\"]\n"
	_, _, indent, ok := blockRange(parse(t, src), "web")
	if !ok {
		t.Fatal("web not found")
	}
	if indent != 4 {
		t.Fatalf("indent = %d, want 4", indent)
	}
}
