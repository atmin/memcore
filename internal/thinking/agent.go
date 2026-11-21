// Package thinking runs your agent headless, for the work that needs judgment: it extracts the corrections
// of a transcript and judges each one. Only claude.go knows Claude Code.
package thinking

// Request is one call of the headless agent.
type Request struct {
	Prompt string // what the agent does with the input
	Input  string // the text on standard input
	Schema string // the JSON schema of the answer
	Model  string // the model of the agent, or "" for its default
}

// Reply is the answer of the headless agent.
type Reply struct {
	Output []byte  // the JSON that the schema asked for
	Cost   float64 // in dollars at API prices; 0 when the agent reports none
}

// Run runs one call of the headless agent. Tests replace it.
var Run func(Request) (Reply, error) = runClaude
