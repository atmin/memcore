// Package thinking runs your agent headless, for the work that needs judgment: it extracts the corrections
// of a transcript and judges each one. claude.go knows Claude Code, and local.go knows the local server.
package thinking

// Request is one call of the headless agent.
type Request struct {
	Prompt   string // what the agent does with the input
	Input    string // the text on standard input
	Schema   string // the JSON schema of the answer
	Model    string // the model of the agent, or "" for its default
	Endpoint string // the base URL of a local model server, or "" for Claude Code
}

// Reply is the answer of the headless agent.
type Reply struct {
	Output []byte  // the JSON that the schema asked for
	Cost   float64 // in dollars at API prices; 0 when the agent reports none
	In     int     // the tokens of the input; 0 when the agent reports none
	Out    int     // the tokens of the answer; 0 when the agent reports none
}

// Run runs one call of the headless agent. Tests replace it.
var Run func(Request) (Reply, error) = run

// run calls the local server for a request with an endpoint, and Claude Code otherwise.
func run(req Request) (Reply, error) {
	if req.Endpoint != "" {
		return runLocal(req)
	}
	return runClaude(req)
}
