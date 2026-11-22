package thinking

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// localTimeout bounds one call of the local server. A test shortens it.
var localTimeout = time.Hour

// localMessage is one message of the chat completions API.
type localMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// localRequest is the body that runLocal posts. Temperature and stream have no omitempty, so the server gets them.
type localRequest struct {
	Model          string         `json:"model"`
	Messages       []localMessage `json:"messages"`
	ResponseFormat struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Strict bool            `json:"strict"`
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	} `json:"response_format"`
	Temperature float64 `json:"temperature"`
	Stream      bool    `json:"stream"`
}

// localReply is the part of the answer of the server that runLocal reads.
type localReply struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// localClient returns the client of one call. It uses no proxy, whatever the environment says, and follows no
// redirect: a redirect is a status other than 200. So the input goes to the endpoint and to no other host.
func localClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil},
		Timeout:       localTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// runLocal runs one call of a local model server, by the chat completions API that local servers share.
// The schema of the request constrains the answer. The call follows no redirect, so the input goes to no other host.
func runLocal(req Request) (Reply, error) {
	body := localRequest{
		Model:    req.Model,
		Messages: []localMessage{{"system", req.Prompt}, {"user", req.Input}},
	}
	body.ResponseFormat.Type = "json_schema"
	body.ResponseFormat.JSONSchema.Name = "answer"
	body.ResponseFormat.JSONSchema.Strict = true
	body.ResponseFormat.JSONSchema.Schema = json.RawMessage(req.Schema)
	data, err := json.Marshal(body)
	if err != nil {
		return Reply{}, fmt.Errorf("write the request of the local model: %w", err)
	}
	url := strings.TrimRight(req.Endpoint, "/") + "/v1/chat/completions"
	client := localClient()
	defer client.CloseIdleConnections()
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return Reply{}, fmt.Errorf("call the local model at %s: %w", req.Endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Reply{}, fmt.Errorf("read the reply of the local model: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		cause := resp.Status
		if l := firstLine(string(raw)); l != "" {
			cause += ": " + l
		}
		return Reply{}, fmt.Errorf("call the local model at %s: it answered %s", req.Endpoint, cause)
	}
	var r localReply
	if err := json.Unmarshal(raw, &r); err != nil {
		return Reply{}, fmt.Errorf("read the reply of the local model: %w", err)
	}
	reply := Reply{In: r.Usage.PromptTokens, Out: r.Usage.CompletionTokens}
	if len(r.Choices) == 0 {
		return reply, fmt.Errorf("read the reply of the local model: it holds no choice")
	}
	content := []byte(r.Choices[0].Message.Content)
	if !json.Valid(content) {
		return reply, fmt.Errorf("read the reply of the local model: its content is not valid JSON: %s", firstLine(string(content)))
	}
	reply.Output = content
	return reply, nil
}
