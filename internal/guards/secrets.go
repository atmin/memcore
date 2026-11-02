package guards

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// secretPatterns are the known shapes of tokens, keys and passwords.
var secretPatterns = []struct {
	kind  string
	re    *regexp.Regexp
	valid func(match string) bool // an extra check of a match, or nil
}{
	{"cloud access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), nil},
	{"cloud access key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), nil},
	{"hosting token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,}|glpat-[A-Za-z0-9_-]{20,})`), nil},
	{"chat token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), nil},
	{"api key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`), nil},
	{"private key", regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`), nil},
	{"bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{8,}=*`), credential},
	{"connection string with a password", regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]*:[^\s@/]+@`), nil},
}

var (
	tokenRE = regexp.MustCompile(`[A-Za-z0-9+/=_-]{32,}`)
	hexRE   = regexp.MustCompile(`^[0-9a-fA-F]+$`)
)

// Secrets finds likely secrets in a text: known token shapes by pattern, and random strings by entropy.
// A finding names the kind and the position, never the secret.
func Secrets(text string) []Finding {
	var findings []Finding
	var taken [][2]int
	add := func(kind string, start, end int) {
		for _, t := range taken {
			if start < t[1] && t[0] < end {
				return
			}
		}
		taken = append(taken, [2]int{start, end})
		line := strings.Count(text[:start], "\n") + 1
		col := utf8.RuneCountInString(text[strings.LastIndex(text[:start], "\n")+1:start]) + 1
		findings = append(findings, Finding{Line: line, Message: fmt.Sprintf("likely secret, a %s, at column %d", kind, col)})
	}
	for _, p := range secretPatterns {
		for _, m := range p.re.FindAllStringIndex(text, -1) {
			if p.valid == nil || p.valid(text[m[0]:m[1]]) {
				add(p.kind, m[0], m[1])
			}
		}
	}
	for _, m := range tokenRE.FindAllStringIndex(text, -1) {
		if random(text[m[0]:m[1]]) {
			add("random string", m[0], m[1])
		}
	}
	return findings
}

// credential reports whether the last word of a match holds a digit or a symbol, as a credential does
// and a word of prose does not. A period at the end belongs to the sentence.
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
