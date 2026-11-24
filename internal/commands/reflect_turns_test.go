package commands

import (
	"strings"
	"testing"
)

// turnBlob joins the lines of a transcript with line ends, and ends it with one.
func turnBlob(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func turnEvent(at, who, text string) string {
	return `{"time":"` + at + `","who":"` + who + `","text":"` + text + `"}`
}

const turnHeader = `{"agent":"claude","session":"s1","dir":"/home/me/dev"}`

func TestReflectTurnSource(t *testing.T) {
	const t1, t2, t3 = "2026-11-23T09:00:00Z", "2026-11-23T09:10:00Z", "2026-11-23T09:20:00Z"
	base := []string{
		turnEvent(t1, "you", "Start here."),
		turnEvent(t2, "you", "Use the project template. Include the impact."),
	}
	with := func(header string, events ...string) string {
		return turnBlob(append([]string{header}, events...)...)
	}
	at := func(time, quote string) reflectCorrection { return reflectCorrection{Time: time, Quote: quote} }
	longText := strings.Repeat("word ", 40000) // over 64 KiB
	same := with(turnHeader, base...)

	tests := []struct {
		name    string
		content string
		query   reflectCorrection
		ok      bool
		sameAs  string // content whose prefix hash must equal this one, when set
		differs string // content whose prefix hash must differ, when set
		otherAt string // the time to query in differs, t2 when empty
	}{
		{name: "plain", content: same, query: at(t2, "Use the project template."), ok: true},
		{name: "other header and session", content: with(`{"agent":"codex","session":"other"}`, base...),
			query: at(t2, "Use the project template."), ok: true, sameAs: same},
		{name: "absent header session", content: with(`{"agent":"claude"}`, base...),
			query: at(t2, "Use the project template."), ok: true, sameAs: same},
		{name: "json spelling", content: with(turnHeader,
			`{ "text" : "Start here.", "who":"you", "time":"`+t1+`", "extra": [1, 2] }`,
			`{"text":"Use the project template. Include the impact.","who":"you","time":"`+t2+`"}`),
			query: at(t2, "Use the project template."), ok: true, sameAs: same},
		{name: "other excerpt of the turn", content: same, query: at(t2, "Include the impact."), ok: true, sameAs: same},
		{name: "folded whitespace", content: same, query: at(t2, "template.\n  Include"), ok: true, sameAs: same},
		{name: "later divergence", content: with(turnHeader, append(append([]string{}, base...),
			turnEvent(t3, "agent", "Different tail."))...),
			query: at(t2, "Include the impact."), ok: true, sameAs: same},
		{name: "broken suffix keeps earlier match", content: with(turnHeader, append(append([]string{}, base...),
			"", "not json", turnEvent(t3, "you", "Later."))...),
			query: at(t2, "Include the impact."), ok: true, sameAs: same},
		{name: "changed prior event", content: with(turnHeader,
			turnEvent(t1, "you", "Start there."), base[1]),
			query: at(t2, "Include the impact."), ok: true, differs: same},
		{name: "changed full turn", content: with(turnHeader, base[0],
			turnEvent(t2, "you", "Use the project template. Include the impact. Add the cost.")),
			query: at(t2, "Include the impact."), ok: true, differs: same},
		{name: "changed speaker", content: with(turnHeader, turnEvent(t1, "agent", "Start here."), base[1]),
			query: at(t2, "Include the impact."), ok: true, differs: same},
		{name: "changed precise time", content: with(turnHeader,
			turnEvent("2026-11-23T09:00:00.5Z", "you", "Start here."), base[1]),
			query: at(t2, "Include the impact."), ok: true, differs: same},
		{name: "changed fractional time of the turn", content: same,
			query: at(t2, "Include the impact."), ok: true,
			differs: with(turnHeader, base[0], turnEvent("2026-11-23T09:10:00.5Z", "you", "Use the project template. Include the impact.")),
			otherAt: "2026-11-23T09:10:00.5Z"},
		{name: "crlf line ends", content: strings.ReplaceAll(same, "\n", "\r\n"),
			query: at(t2, "Include the impact."), ok: true, sameAs: same},
		{name: "extra trailing blank line keeps earlier match", content: same + "\n",
			query: at(t2, "Include the impact."), ok: true, sameAs: same},
		{name: "ambiguous match", content: with(turnHeader, base[0], base[1], base[1]),
			query: at(t2, "Include the impact.")},
		{name: "ambiguous match after a broken line", content: with(turnHeader, base[0], base[1], "", base[1]),
			query: at(t2, "Include the impact.")},
		{name: "missing quote", content: same, query: at(t2, "Absent words.")},
		{name: "empty quote", content: same, query: at(t2, "  ")},
		{name: "other time", content: same, query: at(t3, "Include the impact.")},
		{name: "quote in an agent line", content: with(turnHeader, base[0], turnEvent(t2, "agent", "Include the impact.")),
			query: at(t2, "Include the impact.")},
		{name: "invalid header", content: with("not json", base...), query: at(t2, "Include the impact.")},
		{name: "header is an array", content: with(`[]`, base...), query: at(t2, "Include the impact.")},
		{name: "other agent", content: with(`{"agent":"gemini"}`, base...), query: at(t2, "Include the impact.")},
		{name: "event as header", content: with(base[0], base[1]), query: at(t2, "Include the impact.")},
		{name: "empty object", content: with(turnHeader, base[0], `{}`, base[1]), query: at(t2, "Include the impact.")},
		{name: "second header", content: with(turnHeader, base[0], turnHeader, base[1]), query: at(t2, "Include the impact.")},
		{name: "blank line", content: with(turnHeader, base[0], "", base[1]), query: at(t2, "Include the impact.")},
		{name: "invalid json", content: with(turnHeader, base[0], `{"time":`, base[1]), query: at(t2, "Include the impact.")},
		{name: "missing text", content: with(turnHeader, `{"time":"`+t1+`","who":"you"}`, base[1]),
			query: at(t2, "Include the impact.")},
		{name: "null field", content: with(turnHeader, `{"time":"`+t1+`","who":"you","text":null}`, base[1]),
			query: at(t2, "Include the impact.")},
		{name: "number field", content: with(turnHeader, `{"time":"`+t1+`","who":"you","text":5}`, base[1]),
			query: at(t2, "Include the impact.")},
		{name: "empty time", content: with(turnHeader, turnEvent("", "you", "x"), base[1]),
			query: at(t2, "Include the impact.")},
		{name: "empty speaker", content: with(turnHeader, turnEvent(t1, "", "x"), base[1]),
			query: at(t2, "Include the impact.")},
		{name: "empty content", content: "", query: at(t2, "Include the impact.")},
		{name: "header only", content: turnHeader + "\n", query: at(t2, "Include the impact.")},
		{name: "no final newline", content: strings.TrimSuffix(same, "\n"),
			query: at(t2, "Include the impact."), ok: true, sameAs: same},
		{name: "long turn", content: with(turnHeader, base[0], turnEvent(t2, "you", longText+"tail words")),
			query: at(t2, "tail words"), ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := turnSources(tt.content, []reflectCorrection{tt.query})
			if len(got) != 1 {
				t.Fatalf("got %d sources, want 1", len(got))
			}
			if got[0].OK != tt.ok {
				t.Fatalf("verified = %v, want %v", got[0].OK, tt.ok)
			}
			if !tt.ok {
				if got[0].Prefix != "" || got[0].Text != "" {
					t.Fatalf("an unverified turn has the prefix %q and the text %q", got[0].Prefix, got[0].Text)
				}
				return
			}
			if got[0].Prefix == "" {
				t.Fatal("a verified turn has no prefix")
			}
			if tt.sameAs != "" {
				other := turnSources(tt.sameAs, []reflectCorrection{at(t2, "Include the impact.")})
				if !other[0].OK || other[0].Prefix != got[0].Prefix {
					t.Fatalf("prefix %q differs from %q of the same turn", got[0].Prefix, other[0].Prefix)
				}
			}
			if tt.differs != "" {
				otherAt := t2
				if tt.otherAt != "" {
					otherAt = tt.otherAt
				}
				other := turnSources(tt.differs, []reflectCorrection{at(otherAt, "Include the impact.")})
				if tt.otherAt != "" && !other[0].OK {
					t.Fatal("the other turn is not verified")
				}
				if other[0].Prefix == got[0].Prefix {
					t.Fatalf("prefix %q equals that of another turn", got[0].Prefix)
				}
			}
		})
	}

	t.Run("source text", func(t *testing.T) {
		const spaced = "Use the project template.\n  Include the impact."
		tests := []struct {
			name    string
			content string
			query   reflectCorrection
			want    string // the complete text of the turn, empty when it is ambiguous
		}{
			{"verified", same, at(t2, "Include the impact."), "Use the project template. Include the impact."},
			{"whole turn as the quote", same, at(t2, "Use the project template. Include the impact."),
				"Use the project template. Include the impact."},
			{"unfolded text", with(turnHeader, base[0], turnEvent(t2, "you", `Use the project template.\n  Include the impact.`)),
				at(t2, "template."), spaced},
			{"long turn", with(turnHeader, base[0], turnEvent(t2, "you", longText+"tail words")),
				at(t2, "tail words"), longText + "tail words"},
			{"ambiguous", with(turnHeader, base[0], base[1], base[1]), at(t2, "Include the impact."), ""},
			{"missing quote", same, at(t2, "Absent words."), ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := turnSources(tt.content, []reflectCorrection{tt.query})
				if got[0].Text != tt.want {
					t.Fatalf("text = %q, want %q", got[0].Text, tt.want)
				}
				if (tt.want != "") != got[0].OK {
					t.Fatalf("verified = %v with the text %q", got[0].OK, got[0].Text)
				}
			})
		}
	})

	t.Run("one scan resolves several corrections", func(t *testing.T) {
		content := with(turnHeader, base[0], base[1], turnEvent(t3, "you", "Third."), turnEvent(t3, "you", "Third."),
			"", turnEvent(t2, "you", "Use the project template. Include the impact."))
		got := turnSources(content, []reflectCorrection{
			at(t1, "Start"),       // unique and early
			at(t2, "template"),    // matches twice, the second after the break
			at(t3, "Third"),       // matches twice
			at(t1, "Start here."), // the same turn as the first
			at(t3, "absent"),
		})
		if len(got) != 5 {
			t.Fatalf("got %d sources, want 5", len(got))
		}
		want := []bool{true, false, false, true, false}
		for i, w := range want {
			if got[i].OK != w {
				t.Errorf("query %d verified = %v, want %v", i, got[i].OK, w)
			}
		}
		if got[0].Prefix != got[3].Prefix {
			t.Errorf("one turn has two prefixes: %q and %q", got[0].Prefix, got[3].Prefix)
		}
	})
}
