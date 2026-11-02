package guards

import (
	"strings"
	"testing"
)

func TestSecrets(t *testing.T) {
	// The tokens are built from parts, so no scanner takes this file for a leak.
	tests := []struct {
		name string
		text string
		kind string // "" when the text must pass
	}{
		{"cloud access key", "key " + "AKIA" + "IOSFODNN7EXAMPLE" + " here", "cloud access key"},
		{"cloud api key", "AIza" + strings.Repeat("Sy0_", 8) + "abc", "cloud access key"},
		{"hosting token", "token " + "ghp_" + strings.Repeat("a1B2", 9), "hosting token"},
		{"fine-grained hosting token", "github_pat_" + strings.Repeat("x9", 12), "hosting token"},
		{"gitlab token", "glpat-" + strings.Repeat("Ab3_", 5), "hosting token"},
		{"chat token", "xoxb-" + "1234567890-abcdef", "chat token"},
		{"api key", "sk-" + "ant-" + strings.Repeat("Q7w", 8), "api key"},
		{"private key header", "-----BEGIN " + "OPENSSH PRIVATE KEY-----", "private key"},
		{"bearer token", "Authorization: Bearer " + "abc.def-ghi_jkl.mnopqrstu", "bearer token"},
		{"connection string", "postgres://app:" + "s3cret" + "@db.example.com/shop", "connection string with a password"},
		{"connection string without a user name", "redis://:" + "s3cret" + "@localhost:6379/0", "connection string with a password"},
		{"short bearer token", "Authorization: Bearer " + "mF_9.B5f-4.1JqM", "bearer token"},
		{"bearer token in prose", "The API takes a bearer token in a header.", ""},
		{"bearer at the end of a sentence", "The header holds the bearer credentials.", ""},
		{"random string", "q8ZtV3nRk2LpX9wYb7HcJ4mFs6DgT1aE0uNvQ5yK", "random string"},
		{"commit sha", "Fixed in 9d598ea1b4c3e2f0a7d6c5b4a3928170f6e5d4c3.", ""},
		{"session id", "sessions: claude:d4d1d99a-9439-4b7d-ae7a-25f063f909c9", ""},
		{"plain sentence", "Write the tests of a step before its code, and run them often.", ""},
		{"connection string without a password", "postgres://db.example.com/shop", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Secrets(tt.text)
			if tt.kind == "" {
				if len(got) != 0 {
					t.Fatalf("Secrets = %v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("Secrets = %v, want one %s", got, tt.kind)
			}
			if !strings.Contains(got[0].Message, tt.kind) {
				t.Errorf("finding %q does not name %q", got[0].Message, tt.kind)
			}
			if got[0].Line != 1 || !strings.Contains(got[0].Message, "column") {
				t.Errorf("finding %+v has no position", got[0])
			}
			for _, part := range strings.Fields(tt.text) {
				if len(part) > 12 && strings.Contains(got[0].Message, part) {
					t.Errorf("finding %q holds the secret", got[0].Message)
				}
			}
		})
	}
}

func TestSecretsPosition(t *testing.T) {
	got := Secrets("line one\nthe key " + "AKIA" + "IOSFODNN7EXAMPLE")
	if len(got) != 1 || got[0].Line != 2 || !strings.Contains(got[0].Message, "column 9") {
		t.Errorf("Secrets = %+v, want one at line 2, column 9", got)
	}
}
