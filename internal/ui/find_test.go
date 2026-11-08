package ui

import (
	"reflect"
	"testing"
)

func TestFind(t *testing.T) {
	docs := func(paths ...string) []TreeDoc {
		var d []TreeDoc
		for _, p := range paths {
			d = append(d, TreeDoc{Path: p})
		}
		return d
	}
	paths := func(found []Found) []string {
		var p []string
		for _, f := range found {
			p = append(p, f.Path)
		}
		return p
	}
	tests := []struct {
		name  string
		docs  []TreeDoc
		query string
		limit int
		want  []string
	}{
		{"an empty query finds nothing", docs("a.md"), "", 50, nil},
		{"a query of spaces finds nothing", docs("a.md"), "   ", 50, nil},
		{"a query that is no subsequence finds nothing", docs("plans/roadmap.md"), "xyz", 50, nil},
		{"case does not matter", docs("plans/roadmap.md"), "ROAD", 50, []string{"plans/roadmap.md"}},
		{"spaces in the query do not matter", docs("plans/roadmap.md"), "road map", 50, []string{"plans/roadmap.md"}},
		{"consecutive matches rank first", docs("plans/rowanad.md", "plans/roadmap.md"), "oad", 50, []string{"plans/roadmap.md", "plans/rowanad.md"}},
		{"starts of segments rank first", docs("memories/misc.md", "memories/memcore-state.md"), "ms", 50, []string{"memories/memcore-state.md", "memories/misc.md"}},
		{"the file name ranks first", docs("skills/voice/SKILL.md", "notes/voice.md"), "voice", 50, []string{"notes/voice.md", "skills/voice/SKILL.md"}},
		{"equal scores sort by the shorter path", docs("ab/x.md", "a/x.md"), "x", 50, []string{"a/x.md", "ab/x.md"}},
		{"equal scores and lengths sort by the path", docs("b/x.md", "a/x.md"), "x", 50, []string{"a/x.md", "b/x.md"}},
		{"the limit cuts the list", docs("a/x.md", "b/x.md", "c/x.md"), "x", 2, []string{"a/x.md", "b/x.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := paths(Find(tt.docs, tt.query, tt.limit)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Find(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestFindMarks(t *testing.T) {
	tests := []struct {
		path, query string
		want        []int
	}{
		{"plans/roadmap.md", "road", []int{6, 7, 8, 9}},
		{"notes/éroad.md", "road", []int{7, 8, 9, 10}},
		{"memories/memcore-state.md", "ms", []int{9, 17}},
	}
	for _, tt := range tests {
		found := Find([]TreeDoc{{Path: tt.path}}, tt.query, 50)
		if len(found) != 1 {
			t.Errorf("Find(%q) in %s found %d files, want 1", tt.query, tt.path, len(found))
			continue
		}
		if !reflect.DeepEqual(found[0].Marks, tt.want) {
			t.Errorf("the marks of %q in %s = %v, want %v", tt.query, tt.path, found[0].Marks, tt.want)
		}
	}
}

func TestFindKeepsFields(t *testing.T) {
	found := Find([]TreeDoc{{Path: "a.md", Kind: "memories", Unreviewed: true}}, "a", 50)
	if len(found) != 1 || found[0].Kind != "memories" || !found[0].Unreviewed {
		t.Errorf("Find = %+v, want the kind and the unreviewed mark of a.md", found)
	}
}
