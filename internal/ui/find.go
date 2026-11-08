package ui

import (
	"sort"
	"strings"
	"unicode"
)

// findLimit is the most files that /api/find returns.
const findLimit = 50

// The scores of a matched character in Find.
const (
	scoreMatch       = 1
	scoreSegment     = 8 // at the start of a segment of the path
	scoreConsecutive = 4 // right after the previous match
	scoreName        = 2 // inside the file name
)

// Found is a file that Find matched, with the indexes of the matched characters, counted in runes.
type Found struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Unreviewed bool   `json:"unreviewed"`
	Marks      []int  `json:"marks"`
}

// Finds are the files of main that a query matches, best first.
type Finds struct {
	Main  string  `json:"main"`
	Found []Found `json:"found"`
}

// Find returns at most limit files whose paths hold the query as a subsequence, best first. It ignores case and
// the spaces of the query. Each file scores by the best alignment of the query in its path. Equal scores sort by
// the shorter path, then by the path.
func Find(docs []TreeDoc, query string, limit int) []Found {
	var q []rune
	for _, r := range strings.ToLower(query) {
		if !unicode.IsSpace(r) {
			q = append(q, r)
		}
	}
	if len(q) == 0 || limit <= 0 {
		return nil
	}
	type scored struct {
		Found
		score, runes int
	}
	var all []scored
	for _, d := range docs {
		path := []rune(d.Path)
		if score, marks, ok := align(q, path); ok {
			all = append(all, scored{Found{d.Path, d.Kind, d.Unreviewed, marks}, score, len(path)})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.runes != b.runes {
			return a.runes < b.runes
		}
		return a.Path < b.Path
	})
	if len(all) > limit {
		all = all[:limit]
	}
	found := make([]Found, len(all))
	for i, s := range all {
		found[i] = s.Found
	}
	return found
}

// align finds the best alignment of the lower-case query q in path, by dynamic programming. best[i][j] is the
// best score of q[:i+1] with q[i] at path[j], and from[i][j] is where q[i-1] sits in that alignment.
func align(q, path []rune) (score int, marks []int, ok bool) {
	lower := make([]rune, len(path))
	for i, r := range path {
		lower[i] = unicode.ToLower(r)
	}
	name := 0
	for i, r := range path {
		if r == '/' {
			name = i + 1
		}
	}
	char := func(j int) int {
		s := scoreMatch
		if j == 0 || strings.ContainsRune("/-_. ", path[j-1]) {
			s += scoreSegment
		}
		if j >= name {
			s += scoreName
		}
		return s
	}
	const none = -1
	best := make([][]int, len(q))
	from := make([][]int, len(q))
	for i := range q {
		best[i] = make([]int, len(path))
		from[i] = make([]int, len(path))
		// top is the best score of q[:i] that ends before j-1, and at is where it ends.
		top, at := none, none
		for j := range path {
			best[i][j], from[i][j] = none, none
			if i > 0 && j >= 2 && best[i-1][j-2] > top {
				top, at = best[i-1][j-2], j-2
			}
			if lower[j] != q[i] {
				continue
			}
			if i == 0 {
				best[i][j] = char(j)
				continue
			}
			if top != none {
				best[i][j], from[i][j] = top+char(j), at
			}
			if j >= 1 && best[i-1][j-1] != none && best[i-1][j-1]+scoreConsecutive+char(j) > best[i][j] {
				best[i][j], from[i][j] = best[i-1][j-1]+scoreConsecutive+char(j), j-1
			}
		}
	}
	last := len(q) - 1
	end := none
	for j := range path {
		if best[last][j] != none && (end == none || best[last][j] > best[last][end]) {
			end = j
		}
	}
	if end == none {
		return 0, nil, false
	}
	marks = make([]int, len(q))
	for i, j := last, end; i >= 0; i-- {
		marks[i] = j
		j = from[i][j]
	}
	return best[last][end], marks, true
}
