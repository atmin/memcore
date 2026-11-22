package thinking

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The findings of the judge about one correction.
const (
	None        = "none"        // no memory holds the fact
	Holds       = "holds"       // a memory holds the fact
	Contradicts = "contradicts" // a memory says otherwise
	Unrelated   = "unrelated"   // the correction concerns no memory
)

// Memory is one memory that a session could see.
type Memory struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// Finding is what the judge says about one correction.
type Finding struct {
	Kind   string // None, Holds, Contradicts or Unrelated
	Path   string // the memory of a Holds or a Contradicts finding
	Reason string // one sentence
}

// JudgeRequest asks for one finding for each correction, against the memories that its session could see.
type JudgeRequest struct {
	Corrections []Correction
	Memories    []Memory
	Model       string
	Endpoint    string // the base URL of a local model server, or "" for Claude Code
}

const judgePrompt = `Standard input holds one JSON object. "corrections" lists what a person corrected in a session with a coding agent:
each has an index, the quote of the person and a summary of the rule. "memories" lists the memories that the session
could see: each has a path, a description and a body.

For each correction, decide which of these holds, and give one finding:
- "holds": a memory already states the rule or the fact of the correction. Give the path of that memory.
- "contradicts": a memory states the opposite of the correction, or a fact that the correction makes outdated. Give its path.
- "none": the correction states a rule, a preference or a fact that would help a future session, and no memory holds it.
- "unrelated": the correction concerns only the task of that session, and no future session needs it.

Judge by the bodies of the memories, not by their descriptions. Copy a path exactly. Give one sentence of reason.
Return one finding for every correction, with its index.`

const judgeSchema = `{"type":"object","properties":{"findings":{"type":"array","items":{"type":"object","properties":{"index":{"type":"integer"},"kind":{"type":"string","enum":["holds","contradicts","none","unrelated"]},"path":{"type":"string"},"reason":{"type":"string"}},"required":["index","kind","reason"]}}},"required":["findings"]}`

// Judge returns one finding for each correction, in the order of the corrections, and the cost of the call.
// It checks the path only of a Holds or a Contradicts finding: such a finding becomes Unrelated when its path
// names no memory of the request. A correction that the agent gives no valid finding for is Unrelated too.
func Judge(req JudgeRequest) ([]Finding, float64, error) {
	if len(req.Corrections) == 0 {
		return nil, 0, nil
	}
	type item struct {
		Index   int    `json:"index"`
		Quote   string `json:"quote"`
		Summary string `json:"summary"`
	}
	in := struct {
		Corrections []item   `json:"corrections"`
		Memories    []Memory `json:"memories"`
	}{Memories: req.Memories}
	if in.Memories == nil {
		in.Memories = []Memory{}
	}
	for i, c := range req.Corrections {
		in.Corrections = append(in.Corrections, item{i, c.Quote, c.Summary})
	}
	input, err := json.Marshal(in)
	if err != nil {
		return nil, 0, fmt.Errorf("write the input of the judge: %w", err)
	}
	reply, err := Run(Request{Prompt: judgePrompt, Input: string(input), Schema: judgeSchema, Model: req.Model, Endpoint: req.Endpoint})
	if err != nil {
		return nil, reply.Cost, err
	}
	var out struct {
		Findings []struct {
			Index  *int   `json:"index"`
			Kind   string `json:"kind"`
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(reply.Output, &out); err != nil {
		return nil, reply.Cost, fmt.Errorf("read the findings of the agent: %w", err)
	}
	known := map[string]bool{}
	for _, m := range req.Memories {
		known[m.Path] = true
	}
	findings := make([]Finding, len(req.Corrections))
	for i := range findings {
		findings[i] = Finding{Kind: Unrelated, Reason: "The judge gave no finding."}
	}
	given := map[int]bool{}
	for _, f := range out.Findings {
		if f.Index == nil || *f.Index < 0 || *f.Index >= len(findings) || given[*f.Index] {
			continue
		}
		given[*f.Index] = true
		reason := strings.Join(strings.Fields(f.Reason), " ")
		switch f.Kind {
		case None, Unrelated:
			findings[*f.Index] = Finding{Kind: f.Kind, Reason: reason}
		case Holds, Contradicts:
			if known[f.Path] {
				findings[*f.Index] = Finding{Kind: f.Kind, Path: f.Path, Reason: reason}
			} else {
				findings[*f.Index] = Finding{Kind: Unrelated, Reason: fmt.Sprintf("The judge named %q, which is no memory that the session saw.", f.Path)}
			}
		default:
			findings[*f.Index] = Finding{Kind: Unrelated, Reason: fmt.Sprintf("The judge gave the unknown finding %q.", f.Kind)}
		}
	}
	return findings, reply.Cost, nil
}
