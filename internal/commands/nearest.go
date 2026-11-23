package commands

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atmin/memcore/internal/thinking"
)

// The parameters of BM25.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// stopWords are the words that searchTerms leaves out: English words of grammar, the pieces of contractions, and
// words of talk such as "please". A word of under three runes goes anyway.
var stopWords = setOf(strings.Fields(`about above after again all also and any are aren because been before being
	below both but can could couldn did didn does doesn doing done don each few for from further had hadn has hasn
	have haven having her here hers him his how into isn its just let may might more most must nor not now off once
	only onto other our ours out over own please same shall she should shouldn some such than that the their theirs
	them then there these they this those too under until very via was wasn were weren what when where which while
	who whom why will with won would wouldn yes you your yours`)...)

// searchTerms returns the terms of a text for BM25: its runs of letters and digits, in lower case, of three runes
// or more, without stopWords.
func searchTerms(text string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(w) >= 3 && !stopWords[w] {
			out = append(out, w)
		}
	}
	return out
}

// bm25 returns the BM25 score of each document for a query. Each distinct term of the query counts once. The idf
// is ln(1 + (N - n + 0.5) / (n + 0.5)), which stays positive.
func bm25(query string, docs []string) []float64 {
	counts := make([]map[string]int, len(docs))
	lengths := make([]int, len(docs))
	df := map[string]int{}
	total := 0
	for i, d := range docs {
		counts[i] = map[string]int{}
		for _, t := range searchTerms(d) {
			counts[i][t]++
			lengths[i]++
		}
		for t := range counts[i] {
			df[t]++
		}
		total += lengths[i]
	}
	scores := make([]float64, len(docs))
	seen := map[string]bool{}
	for _, q := range searchTerms(query) {
		if seen[q] || df[q] == 0 {
			continue
		}
		seen[q] = true
		n, all := float64(df[q]), float64(len(docs))
		idf := math.Log(1 + (all-n+0.5)/(n+0.5))
		avg := float64(total) / all
		for i := range docs {
			f := float64(counts[i][q])
			if f == 0 {
				continue
			}
			scores[i] += idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(lengths[i])/avg))
		}
	}
	return scores
}

// scoreMemories scores each memory by the terms in its description and body.
func scoreMemories(query string, memories []thinking.Memory) []float64 {
	docs := make([]string, len(memories))
	for i, m := range memories {
		docs[i] = m.Description + " " + m.Body
	}
	return bm25(query, docs)
}

// rankMemories returns the indices of memories by their BM25 score for a query, over the description and the body
// of each. Equal scores go by path.
func rankMemories(query string, memories []thinking.Memory) []int {
	return orderByScore(scoreMemories(query, memories), memories)
}

func orderByScore(scores []float64, memories []thinking.Memory) []int {
	order := make([]int, len(memories))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		if scores[order[a]] != scores[order[b]] {
			return scores[order[a]] > scores[order[b]]
		}
		return memories[order[a]].Path < memories[order[b]].Path
	})
	return order
}

const nearestCount = 5

// nearestMemories keeps every read memory and the nearest matches of each correction, in input order.
func nearestMemories(corrections []thinking.Correction, memories []thinking.Memory, read map[string]bool) []thinking.Memory {
	if len(memories) <= nearestCount {
		return memories
	}
	chosen := make([]bool, len(memories))
	for _, c := range corrections {
		scores := scoreMemories(c.Quote+" "+c.Summary, memories)
		order := orderByScore(scores, memories)
		for _, i := range order[:nearestCount] {
			if scores[i] == 0 {
				break
			}
			chosen[i] = true
		}
	}
	var out []thinking.Memory
	for i, m := range memories {
		if chosen[i] || read[m.Path] {
			out = append(out, m)
		}
	}
	return out
}

// readPaths returns the documents read by the session at or before until.
func readPaths(events []Event, session string, until time.Time) map[string]bool {
	read := map[string]bool{}
	for _, e := range events {
		if e.Session == session && !eventTime(e).After(until) {
			for _, path := range readsOf(e) {
				read[path] = true
			}
		}
	}
	return read
}
