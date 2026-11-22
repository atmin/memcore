// Command reflect-eval compares a local model with the records of the reflect passes. It runs the two parts of a
// pass, extract and judge, with the model over the transcripts that passes already judged, and prints how often
// the model agrees with the records. The records are a reference, not the truth.
//
// The check reads the memory repo, and writes only results.jsonl in the folder of --out. It sends the
// transcripts only to a server on this machine. Start the model in a local server that serves the chat
// completions API, set its context there, and give the same context here:
//
//	go run ./scripts/reflect-eval --endpoint http://127.0.0.1:<port> --model <model> --context 65536 --out <new folder>
//
// Start with --sessions 3. A large model can exhaust the memory of the machine, so start with a small one.
// No test runs this command, and memcore never calls it.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/atmin/memcore/internal/commands"
)

func main() {
	var e commands.ReflectEval
	flag.StringVar(&e.Endpoint, "endpoint", "", "the base URL of the local model server, on this machine")
	flag.StringVar(&e.Model, "model", "", "the model that the server runs")
	flag.IntVar(&e.Context, "context", 32768, "the context of the model, in tokens, as the server sets it")
	flag.StringVar(&e.Out, "out", "", "a new folder for results.jsonl, outside the memory repo")
	flag.IntVar(&e.Sessions, "sessions", 0, "the most records that the check reads; 0 for all")
	flag.StringVar(&e.Stage, "stage", "", "extract or judge, to run one part only")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "reflect-eval: no argument besides the flags")
		flag.Usage()
		os.Exit(2)
	}
	if err := commands.RunReflectEval(e, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "reflect-eval:", err)
		os.Exit(1)
	}
}
