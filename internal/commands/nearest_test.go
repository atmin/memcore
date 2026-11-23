package commands

import (
	"math"
	"slices"
	"testing"

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
