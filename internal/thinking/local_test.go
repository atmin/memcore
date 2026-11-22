package thinking

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// localServer starts a test server that answers each call with handle.
func localServer(t *testing.T, handle http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handle)
	t.Cleanup(srv.Close)
	return srv
}

// completion writes a reply of the chat completions API with one choice.
func completion(w http.ResponseWriter, content string, in, out int) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]any{"prompt_tokens": in, "completion_tokens": out},
	})
}

func TestRunLocal(t *testing.T) {
	for _, suffix := range []string{"", "/"} {
		t.Run("endpoint"+suffix, func(t *testing.T) {
			var body map[string]json.RawMessage
			srv := localServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					t.Errorf("request = %s %s, want POST /v1/chat/completions", r.Method, r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("read the body: %v", err)
				}
				completion(w, `{"corrections":[]}`, 120, 8)
			})
			reply, err := runLocal(Request{Prompt: "List them.", Input: "the transcript", Schema: `{"type":"object"}`, Model: "small", Endpoint: srv.URL + suffix})
			if err != nil {
				t.Fatal(err)
			}
			if string(reply.Output) != `{"corrections":[]}` || reply.In != 120 || reply.Out != 8 || reply.Cost != 0 {
				t.Errorf("reply = %q, in %d, out %d, cost %v", reply.Output, reply.In, reply.Out, reply.Cost)
			}
			want := map[string]string{
				"model":           `"small"`,
				"messages":        `[{"role":"system","content":"List them."},{"role":"user","content":"the transcript"}]`,
				"response_format": `{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object"}}}`,
				"temperature":     `0`,
				"stream":          `false`,
			}
			for key, w := range want {
				if got := string(body[key]); got != w {
					t.Errorf("%s = %s, want %s", key, got, w)
				}
			}
			if len(body) != len(want) {
				t.Errorf("the body has %d fields, want %d", len(body), len(want))
			}
		})
	}
}

func TestRunLocalErrors(t *testing.T) {
	var elsewhere atomic.Int32
	other := localServer(t, func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Add(1)
		completion(w, `{}`, 1, 1)
	})
	cases := []struct {
		name    string
		handle  http.HandlerFunc // nil: no server
		wantErr string
	}{
		{"no connection", nil, "call the local model"},
		{"a status other than 200", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "model not loaded\nmore", http.StatusServiceUnavailable)
		}, "503 Service Unavailable: model not loaded"},
		{"a redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
		}, "it answered 307 Temporary Redirect"},
		{"no choice", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":3}}`))
		}, "no choice"},
		{"content that is not JSON", func(w http.ResponseWriter, r *http.Request) {
			completion(w, "Sure! Here are the corrections.", 3, 5)
		}, "not valid JSON"},
		{"a call over the time", func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body) // so the server sees the client go
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}, "Timeout exceeded"},
	}
	prev := localTimeout
	t.Cleanup(func() { localTimeout = prev })
	localTimeout = 200 * time.Millisecond
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			endpoint := "http://127.0.0.1:0" // no server listens on port 0
			if c.handle != nil {
				endpoint = localServer(t, c.handle).URL
			}
			_, err := runLocal(Request{Prompt: "p", Input: "i", Schema: "{}", Model: "m", Endpoint: endpoint})
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want one with %q", err, c.wantErr)
			}
		})
	}
	if n := elsewhere.Load(); n != 0 {
		t.Errorf("the redirect reached the other server %d times, want 0", n)
	}
}

func TestRunPicksTheAgent(t *testing.T) {
	var served atomic.Int32
	srv := localServer(t, func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		io.Copy(io.Discard, r.Body)
		completion(w, `{"a":1}`, 1, 1)
	})
	prev := lookPath
	t.Cleanup(func() { lookPath = prev })
	var looked int
	lookPath = func(string) (string, error) {
		looked++
		return "", errors.New("not found")
	}

	if _, err := Run(Request{Prompt: "p", Schema: "{}", Endpoint: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if served.Load() != 1 || looked != 0 {
		t.Errorf("a request with an endpoint: server %d, lookPath %d; want 1 and 0", served.Load(), looked)
	}
	if _, err := Run(Request{Prompt: "p", Schema: "{}"}); err == nil {
		t.Error("a request without an endpoint ran, want the error of lookPath")
	}
	if served.Load() != 1 || looked != 1 {
		t.Errorf("a request without an endpoint: server %d, lookPath %d; want 1 and 1", served.Load(), looked)
	}
}

// TestLocalClientUsesNoProxy: the input goes straight to the local server, and never to a proxy of the environment.
func TestLocalClientUsesNoProxy(t *testing.T) {
	transport, ok := localClient().Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Errorf("the transport of the client is %T with a proxy function, want an *http.Transport with none", localClient().Transport)
	}
	if localClient().CheckRedirect == nil {
		t.Error("the client follows redirects")
	}
}
