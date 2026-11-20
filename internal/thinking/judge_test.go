package thinking

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestJudge(t *testing.T) {
	corrections := []Correction{
		{"t0", "Use small commits.", "Commit small."},
		{"t1", "Never push.", "Ask before a push."},
		{"t2", "We use tabs now.", "Indent with tabs."},
		{"t3", "Not that file.", "A detail of the task."},
		{"t4", "Use the staging host.", "Use staging."},
		{"t5", "Run the linter.", "Lint before a commit."},
		{"t6", "Keep it short.", "Write short replies."},
		{"t7", "Twice.", "Answered twice."},
	}
	memories := []Memory{
		{"dev/memories/push.md", "Read before you push.", "Push only when asked."},
		{"memories/indent.md", "Read before you format.", "Indent with spaces."},
	}

	t.Run("each finding", func(t *testing.T) {
		calls := fakeRun(t, answer(`{"findings":[
			{"index":1,"kind":"holds","path":"dev/memories/push.md","reason":"The memory  says so."},
			{"index":0,"kind":"none","reason":"No memory holds it."},
			{"index":2,"kind":"contradicts","path":"memories/indent.md","reason":"The memory says spaces."},
			{"index":3,"kind":"unrelated","path":"dev/memories/push.md","reason":"Only this task."},
			{"index":4,"kind":"holds","path":"dev/memories/hosts.md","reason":"A path outside the input."},
			{"index":5,"kind":"none","path":"dev/memories/absent.md","reason":"A none finding checks no path."},
			{"index":6,"kind":"maybe","reason":"An unknown kind."},
			{"index":7,"kind":"none","reason":"First."},
			{"index":7,"kind":"unrelated","reason":"Second."},
			{"index":9,"kind":"none","reason":"No such correction."},
			{"kind":"none","reason":"No index."}
		]}`, 0.3))
		got, cost, err := Judge(JudgeRequest{Corrections: corrections, Memories: memories, Model: "sonnet", Budget: 1})
		if err != nil {
			t.Fatal(err)
		}
		want := []Finding{
			{None, "", "No memory holds it."},
			{Holds, "dev/memories/push.md", "The memory says so."},
			{Contradicts, "memories/indent.md", "The memory says spaces."},
			{Unrelated, "", "Only this task."},
			{Unrelated, "", `The judge named "dev/memories/hosts.md", which is no memory that the session saw.`},
			{None, "", "A none finding checks no path."},
			{Unrelated, "", `The judge gave the unknown finding "maybe".`},
			{None, "", "First."},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("findings:\n got %+v\nwant %+v", got, want)
		}
		if cost != 0.3 {
			t.Errorf("cost = %v, want 0.3", cost)
		}
		req := (*calls)[0]
		if req.Model != "sonnet" || req.Budget != 1 || req.Schema != judgeSchema || req.Prompt != judgePrompt {
			t.Errorf("request = %+v", req)
		}
		var in struct {
			Corrections []struct {
				Index   int
				Quote   string
				Summary string
			}
			Memories []Memory
		}
		if err := json.Unmarshal([]byte(req.Input), &in); err != nil {
			t.Fatal(err)
		}
		if len(in.Corrections) != len(corrections) || in.Corrections[2].Index != 2 || in.Corrections[2].Quote != "We use tabs now." || !reflect.DeepEqual(in.Memories, memories) {
			t.Errorf("input = %+v", in)
		}
	})

	t.Run("a missing finding", func(t *testing.T) {
		fakeRun(t, answer(`{"findings":[]}`, 0))
		got, _, err := Judge(JudgeRequest{Corrections: corrections[:1]})
		if err != nil || len(got) != 1 || got[0].Kind != Unrelated {
			t.Errorf("got %+v, %v; want one unrelated finding", got, err)
		}
	})

	t.Run("no correction", func(t *testing.T) {
		fakeRun(t)
		if got, cost, err := Judge(JudgeRequest{Memories: memories}); err != nil || got != nil || cost != 0 {
			t.Errorf("got %v, %v, %v; want nothing and no call", got, cost, err)
		}
	})

	t.Run("budget error", func(t *testing.T) {
		fakeRun(t, func(Request) (Reply, error) { return Reply{Cost: 0.2}, ErrBudget })
		if got, cost, err := Judge(JudgeRequest{Corrections: corrections}); !errors.Is(err, ErrBudget) || got != nil || cost != 0.2 {
			t.Errorf("got %v, %v, %v; want ErrBudget with the cost", got, cost, err)
		}
	})
}
