package store

import (
	"errors"
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
		{
			name: "missing sessions",
			in:   without("sessions"),
			want: Memory{
				Name:        "tests-before-code",
				Description: "Read before you write code, since the tests of a step come first.",
				Type:        "feedback",
				Body:        "Write the tests of a step before its code.\n",
			},
		},
		{
			name: "sessions as a flow list",
			in:   replace("sessions:\n  - claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9", `sessions: [claude:one, "claude:two"]`),
			want: Memory{
				Name:        "tests-before-code",
				Description: "Read before you write code, since the tests of a step come first.",
				Type:        "feedback",
				Sessions:    []string{"claude:one", "claude:two"},
				Body:        "Write the tests of a step before its code.\n",
			},
		},
		{name: "extra key", in: replace("type: feedback", "type: feedback\nowner: me"), wantErr: `unknown key "owner"`},
		{name: "bad name", in: replace("name: tests-before-code", "name: Tests_Before"), wantErr: `name "Tests_Before"`},
		{name: "bad type", in: replace("type: feedback", "type: opinion"), wantErr: `type "opinion"`},
		{
			name: "plain value with a colon",
			in:   replace("since the tests", "since: the tests"),
			want: Memory{
				Name:        "tests-before-code",
				Description: "Read before you write code, since: the tests of a step come first.",
				Type:        "feedback",
				Sessions:    []string{"claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9"},
				Body:        "Write the tests of a step before its code.\n",
			},
		},
		{
			name: "single-quoted value",
			in:   replace("type: feedback", "type: 'feedback'"),
			want: Memory{
				Name:        "tests-before-code",
				Description: "Read before you write code, since the tests of a step come first.",
				Type:        "feedback",
				Sessions:    []string{"claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9"},
				Body:        "Write the tests of a step before its code.\n",
			},
		},
		{
			name: "single-quoted description with a quote inside",
			in:   replace("description: Read before you write code, since the tests of a step come first.", "description: 'Read before you write code: it''s the rule # one.'"),
			want: Memory{
				Name:        "tests-before-code",
				Description: "Read before you write code: it's the rule # one.",
				Type:        "feedback",
				Sessions:    []string{"claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9"},
				Body:        "Write the tests of a step before its code.\n",
			},
		},
		{name: "a nested map", in: replace("type: feedback", "type: feedback\nmetadata:\n  origin: auto"), wantErr: "nested map"},
		{name: "an unclosed single quote", in: replace("type: feedback", "type: 'feedback"), wantErr: "single quote"},
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

func TestFrontmatterBytes(t *testing.T) {
	in := "---\nname: build-x\ndescription: Read when you build x: the first step.\nowner: 'me'\nnote: plain words\ntags: []\nsessions:\n  - claude:one\n---\n\nThe body.\n"
	fm, body, err := ParseFrontmatter(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: build-x\ndescription: \"Read when you build x: the first step.\"\nowner: me\nnote: plain words\ntags: []\nsessions:\n  - claude:one\n---\n"
	if got := string(fm.Bytes()); got != want {
		t.Errorf("Bytes() =\n%s\nwant\n%s", got, want)
	}
	again, againBody, err := ParseFrontmatter(strings.NewReader(string(fm.Bytes()) + "\n" + body))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if !reflect.DeepEqual(again, fm) || againBody != body {
		t.Errorf("round trip = %#v, %q, want %#v, %q", again, againBody, fm, body)
	}
	for _, v := range []string{"a: b", "a #b", "ends with:", "'quoted'", "- item", "[x]", " padded", "plain"} {
		got := formatValue(v)
		strict := !strings.HasPrefix(got, `"`)
		if strict != (v == "plain") {
			t.Errorf("formatValue(%q) = %s, want double quotes for every value but plain", v, got)
		}
	}
}

func TestParseFlow(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{`[a, b]`, []string{"a", "b"}},
		{`[it's, fine]`, []string{"it's", "fine"}},
		{`["a, b", 'c''d']`, []string{"a, b", "c'd"}},
		{`[ ]`, nil},
		{`['it''s, fine']`, []string{"it's, fine"}},
	}
	for _, tt := range tests {
		got, err := parseFlow(tt.in)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseFlow(%s) = %q, %v, want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestInputErrorRedacted(t *testing.T) {
	_, _, err := ParseFrontmatter(strings.NewReader("---\nprivate_token: [,]\n---\n"))
	var ie *InputError
	if !errors.As(err, &ie) {
		t.Fatalf("error = %v, want an InputError", err)
	}
	if r := ie.Redacted(); strings.Contains(r, "private_token") || !strings.Contains(r, "frontmatter line 2") {
		t.Errorf("Redacted() = %q, want the line without the key", r)
	}
}
