package guards

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// secretPatterns are the known shapes of tokens, keys and passwords.
var secretPatterns = []struct {
	kind   string
	re     *regexp.Regexp
	valid  func(match string) bool        // an extra check of a match, or nil
	extend func(text string, end int) int // the end of the whole secret after a match, or nil
}{
	{"cloud access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), nil, nil},
	{"cloud access key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), nil, nil},
	{"hosting token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,}|glpat-[A-Za-z0-9_-]{20,})`), nil, nil},
	{"chat token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), nil, nil},
	{"api key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`), nil, nil},
	{"private key", regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`), nil, keyBlock},
	{"bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{8,}=*`), credential, nil},
	{"connection string with a password", regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]*:[^\s@/]+@`), nil, nil},
}

var (
	tokenRE  = regexp.MustCompile(`[A-Za-z0-9+/=_-]{32,}`)
	hexRE    = regexp.MustCompile(`^[0-9a-fA-F]+$`)
	keyEndRE = regexp.MustCompile(`-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----`)
)

// span is one likely secret: its kind, and where it starts and ends in the text.
type span struct {
	kind       string
	start, end int
}

// scan finds the likely secrets in a text, in the order of the patterns, then the random strings.
// Spans may overlap, as a key inside a connection string does.
func scan(text string) []span {
	var spans []span
	add := func(kind string, start, end int) {
		spans = append(spans, span{kind, start, end})
	}
	for _, p := range secretPatterns {
		for _, m := range p.re.FindAllStringIndex(text, -1) {
			if p.valid != nil && !p.valid(text[m[0]:m[1]]) {
				continue
			}
			end := m[1]
			if p.extend != nil {
				end = p.extend(text, end)
			}
			add(p.kind, m[0], end)
		}
	}
	for _, m := range tokenRE.FindAllStringIndex(text, -1) {
		if random(text[m[0]:m[1]]) {
			add("random string", m[0], m[1])
		}
	}
	return spans
}

// Secrets finds likely secrets in a text: known token shapes by pattern, and random strings by entropy.
// A finding names the kind and the position, never the secret.
func Secrets(text string) []Finding {
	var findings []Finding
	var kept []span
	for _, s := range scan(text) {
		// The first finding keeps its place, so one secret gives one finding.
		if slices.ContainsFunc(kept, func(k span) bool { return s.start < k.end && k.start < s.end }) {
			continue
		}
		kept = append(kept, s)
		line := strings.Count(text[:s.start], "\n") + 1
		col := utf8.RuneCountInString(text[strings.LastIndex(text[:s.start], "\n")+1:s.start]) + 1
		findings = append(findings, Finding{Line: line, Message: fmt.Sprintf("likely secret, a %s, at column %d", s.kind, col)})
	}
	return findings
}

// Redact replaces each likely secret in a text with a placeholder that names its kind, as [secret:api-key].
// Overlapping secrets merge into one placeholder, with the kind of the one that starts first.
func Redact(text string) string {
	spans := scan(text)
	slices.SortStableFunc(spans, func(a, b span) int { return a.start - b.start })
	var merged []span
	for _, s := range spans {
		if n := len(merged); n > 0 && s.start < merged[n-1].end {
			merged[n-1].end = max(merged[n-1].end, s.end)
			continue
		}
		merged = append(merged, s)
	}
	var b strings.Builder
	last := 0
	for _, s := range merged {
		b.WriteString(text[last:s.start])
		b.WriteString("[secret:" + strings.ReplaceAll(s.kind, " ", "-") + "]")
		last = s.end
	}
	b.WriteString(text[last:])
	return b.String()
}

// keyBlock returns the end of a private key block, which is the end of its END line.
// Without an END line, the block ends at the end of the text.
func keyBlock(text string, end int) int {
	if m := keyEndRE.FindStringIndex(text[end:]); m != nil {
		return end + m[1]
	}
	return len(text)
}

// credential reports whether the last word of a match holds a digit or a symbol.
// A credential holds one, and a word of prose does not. A period at the end belongs to the sentence.
func credential(match string) bool {
	f := strings.Fields(match)
	return strings.ContainsAny(strings.TrimRight(f[len(f)-1], "."), "0123456789._~+/-")
}

// random reports whether a token looks random: letters of both cases and digits, and above 4.5 bits per character.
// A hex-only token, such as a commit sha, is not random here.
func random(token string) bool {
	if hexRE.MatchString(token) {
		return false
	}
	var upper, lower, digit bool
	counts := map[rune]int{}
	for _, r := range token {
		counts[r]++
		switch {
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= '0' && r <= '9':
			digit = true
		}
	}
	if !upper || !lower || !digit {
		return false
	}
	n := float64(len(token))
	bits := 0.0
	for _, c := range counts {
		p := float64(c) / n
		bits -= p * math.Log2(p)
	}
	return bits > 4.5
}
