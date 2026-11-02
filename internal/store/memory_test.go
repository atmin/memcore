package store

import (
	"reflect"
	"strings"
	"testing"
)

const validMemory = `---
name: tests-before-code
description: Read before you write code, since the tests of a step come first.
type: feedback
sessions:
  - claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9
---

Write the tests of a step before its code.
`

func TestParseMemory(t *testing.T) {
	without := func(key string) string {
		var kept []string
		skip := false
		for _, line := range strings.Split(validMemory, "\n") {
			if strings.HasPrefix(line, "  - ") && skip {
				continue
			}
			skip = strings.HasPrefix(line, key+":")
			if !skip {
				kept = append(kept, line)
			}
		}
		return strings.Join(kept, "\n")
	}
	replace := func(old, new string) string { return strings.Replace(validMemory, old, new, 1) }

	tests := []struct {
		name    string
		in      string
		want    Memory
		wantErr string
	}{
		{
			name: "valid",
			in:   validMemory,
			want: Memory{
				Name:        "tests-before-code",
				Description: "Read before you write code, since the tests of a step come first.",
				Type:        "feedback",
				Sessions:    []string{"claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9"},
				Body:        "Write the tests of a step before its code.\n",
			},
		},
		{
			name: "quoted description with a colon",
			in:   replace("description: Read before you write code, since the tests of a step come first.", `description: "Read before you write code: \"tests\" first, since the tests of a step come first."`),
			want: Memory{
				Name:        "tests-before-code",
				Description: `Read before you write code: "tests" first, since the tests of a step come first.`,
				Type:        "feedback",
				Sessions:    []string{"claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9"},
				Body:        "Write the tests of a step before its code.\n",
			},
		},
		{
			name: "empty sessions",
			in:   replace("sessions:\n  - claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9", "sessions: []"),
			want: Memory{
				Name:        "tests-before-code",
				Description: "Read before you write code, since the tests of a step come first.",
				Type:        "feedback",
				Body:        "Write the tests of a step before its code.\n",
			},
		},
		{name: "missing name", in: without("name"), wantErr: `missing key "name"`},
		{name: "missing description", in: without("description"), wantErr: `missing key "description"`},
		{name: "missing type", in: without("type"), wantErr: `missing key "type"`},
		{name: "missing sessions", in: without("sessions"), wantErr: `missing key "sessions"`},
		{name: "extra key", in: replace("type: feedback", "type: feedback\nowner: me"), wantErr: `unknown key "owner"`},
		{name: "bad name", in: replace("name: tests-before-code", "name: Tests_Before"), wantErr: `name "Tests_Before"`},
		{name: "bad type", in: replace("type: feedback", "type: opinion"), wantErr: `type "opinion"`},
		{name: "plain value with a colon", in: replace("since the tests", "since: the tests"), wantErr: "double quotes"},
		{name: "single-quoted value", in: replace("type: feedback", "type: 'feedback'"), wantErr: "double quotes"},
		{name: "a sequence for a value", in: replace("description: Read", "description: - Read"), wantErr: "double quotes"},
		{name: "a bad escape", in: replace("description: Read before you write code, since the tests of a step come first.", `description: "Read when \q."`), wantErr: "escape"},
		{name: "no frontmatter", in: "Write the tests first.\n", wantErr: "no frontmatter"},
		{name: "unclosed frontmatter", in: "---\nname: a\n", wantErr: "no closing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMemory(strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one that holds %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("memory = %#v, want %#v", got, tt.want)
			}

			again, err := ParseMemory(strings.NewReader(string(got.Bytes())))
			if err != nil {
				t.Fatalf("round trip: %v\n%s", err, got.Bytes())
			}
			if !reflect.DeepEqual(again, got) {
				t.Errorf("round trip = %#v, want %#v", again, got)
			}
		})
	}
}

func TestMemoryBytes(t *testing.T) {
	m := Memory{
		Name:        "a-fact",
		Description: "Read when you test: a colon stays inside.",
		Type:        "project",
		Body:        "The fact.\n",
	}
	want := `---
name: a-fact
description: "Read when you test: a colon stays inside."
type: project
sessions: []
---

The fact.
`
	if got := string(m.Bytes()); got != want {
		t.Errorf("Bytes() =\n%s\nwant\n%s", got, want)
	}
}

func TestYAMLQuotes(t *testing.T) {
	tests := []struct{ quoted, want string }{
		{`"a \"b\" c"`, `a "b" c`},
		{`"caf\xE9"`, "café"},
		{`"café and \U0001F600"`, "café and 😀"},
		{`"a\/b\\c"`, `a/b\c`},
		{`"tab\there\nline"`, "tab\there\nline"},
	}
	for _, tt := range tests {
		got, err := parseValue(tt.quoted)
		if err != nil || got != tt.want {
			t.Errorf("parseValue(%s) = %q, %v; want %q", tt.quoted, got, err, tt.want)
		}
		back, err := parseValue(quote(got))
		if err != nil || back != got {
			t.Errorf("parseValue(quote(%q)) = %q, %v", got, back, err)
		}
	}
	for _, v := range []string{"claude:abc", "a: b", "line\nbreak", "- item", " padded"} {
		back, err := parseValue(formatValue(v))
		if err != nil || back != v {
			t.Errorf("parseValue(formatValue(%q)) = %q, %v", v, back, err)
		}
	}
}

func TestMemoryWarnings(t *testing.T) {
	tests := []struct {
		description string
		want        int
	}{
		{"Read when you write code.", 0},
		{"Read before you push.", 0},
		{"The tests of a step.", 1},
		{"Read when you " + strings.Repeat("word ", 25), 1},
		{"The " + strings.Repeat("word ", 25), 2},
	}
	for _, tt := range tests {
		m := Memory{Description: tt.description}
		if got := m.Warnings(); len(got) != tt.want {
			t.Errorf("Warnings(%q) = %q, want %d", tt.description, got, tt.want)
		}
	}
}
