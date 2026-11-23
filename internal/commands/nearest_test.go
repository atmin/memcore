package commands

import (
	"maps"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/atmin/memcore/internal/thinking"
)

func TestBM25(t *testing.T) {
	if got, want := searchTerms("Don't push the Branch, café-42 ok, Ünïcode"), []string{"push", "branch", "café", "ünïcode"}; !slices.Equal(got, want) {
		t.Errorf("searchTerms = %q, want %q", got, want)
	}
	// One term of a corpus of three: the idf is ln(1.6), and the average length is 2.
	scores := bm25("apple", []string{"apple banana", "apple apple cherry", "durian"})
	idf := math.Log(1.6)
	want := []float64{idf, idf * 4.4 / 3.65, 0}
	for i := range want {
		if math.Abs(scores[i]-want[i]) > 1e-9 {
			t.Errorf("score %d = %v, want %v", i, scores[i], want[i])
		}
	}

	memories := []thinking.Memory{
		{Path: "dev/memories/a.md", Description: "Ask before a push.", Body: "Push only when the person asks."},
		{Path: "dev/memories/b.md", Description: "Commit small.", Body: "Commit one change at a time. Keep each commit small."},
		{Path: "dev/memories/c.md", Description: "Retry fetches.", Body: "Retry a failed fetch three times."},
	}
	for _, c := range []struct {
		query string
		want  []int
	}{
		{"Never push the branch.", []int{0, 1, 2}},
		{"Use a small change.", []int{1, 0, 2}},
		{"Retry the fetch.", []int{2, 0, 1}},
		{"Nothing matches here.", []int{0, 1, 2}}, // equal scores go by path
	} {
		if got := rankMemories(c.query, memories); !slices.Equal(got, c.want) {
			t.Errorf("rankMemories(%q) = %v, want %v", c.query, got, c.want)
		}
	}
}

func TestNearestMemories(t *testing.T) {
	memories := []thinking.Memory{
		{Path: "z", Body: "apple pear"},
		{Path: "c", Body: "apple peach"},
		{Path: "a", Body: "apple seed"},
		{Path: "e", Body: "apple seed"},
		{Path: "d", Body: "apple seed"},
		{Path: "b", Body: "apple seed"},
		{Path: "g", Description: "peach"},
		{Path: "h", Body: "olive"},
	}
	apple := thinking.Correction{Quote: "apple"}
	peach := thinking.Correction{Summary: "peach"}
	miss := thinking.Correction{Quote: "banana"}
	for _, tc := range []struct {
		name        string
		count       int
		corrections []thinking.Correction
		read        map[string]bool
		want        []string
	}{
		{"all five without a match", 5, []thinking.Correction{miss}, nil, []string{"z", "c", "a", "e", "d"}},
		{"five nearest in input order", 8, []thinking.Correction{apple}, nil, []string{"c", "a", "e", "d", "b"}},
		{"quote and summary change the top five", 8, []thinking.Correction{{Quote: "apple", Summary: "pear"}}, nil, []string{"z", "c", "a", "d", "b"}},
		{"overlapping corrections", 8, []thinking.Correction{apple, peach}, nil, []string{"c", "a", "e", "d", "b", "g"}},
		{"read paths on this level path", 8, []thinking.Correction{peach}, setOf("h", "g", "elsewhere"), []string{"c", "g", "h"}},
		{"one correction without a match", 8, []thinking.Correction{peach, miss}, nil, []string{"c", "g"}},
		{"no matches", 8, []thinking.Correction{miss}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			for _, m := range nearestMemories(tc.corrections, memories[:tc.count], tc.read) {
				paths = append(paths, m.Path)
			}
			if !slices.Equal(paths, tc.want) {
				t.Errorf("chosen paths = %v, want %v", paths, tc.want)
			}
		})
	}
}

func TestReadPaths(t *testing.T) {
	until := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	before := until.Add(-time.Second).Format(time.RFC3339)
	events := []Event{
		{Session: "s", Time: before, Command: "show", Outcome: "ok", Paths: []string{"early"}},
		{Session: "s", Time: until.Add(time.Second).Format(time.RFC3339), Command: "show", Outcome: "ok", Paths: []string{"late"}},
		{Session: "other", Time: before, Command: "show", Outcome: "ok", Paths: []string{"other"}},
		{Session: "s", Time: before, Command: "show", Outcome: "error", Paths: []string{"failed"}},
		{Session: "s", Time: until.Format(time.RFC3339), Command: "hook", Hook: "tool", Paths: []string{"early", "at cutoff"}},
	}
	if got, want := readPaths(events, "s", until), setOf("early", "at cutoff"); !maps.Equal(got, want) {
		t.Errorf("read paths = %v, want %v", got, want)
	}
}
