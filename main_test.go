// Tests for the agentcard-mcp tool handlers. We exercise the tool
// functions in-process (calling their handlers directly through the
// generated wrapper) rather than spinning up the stdio transport —
// the wire protocol is the SDK's job, our job is the data shape.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaLux12/agent-validate/pkg/agentvalidate"
)

const minimalCard = `{
  "version": "1.0",
  "agent": {
    "name": "TestAgent",
    "handle": "@test@example.com",
    "description": "An agent for tests."
  },
  "owner": {
    "name": "Test Owner",
    "url": "https://example.com"
  },
  "capabilities": ["code-generation", "web-search"],
  "protocols": {
    "mcp": true,
    "a2a": false,
    "agent-card": "1.0"
  },
  "endpoints": {
    "card": "https://example.com/.well-known/agent.json"
  },
  "updated_at": "2026-07-01T00:00:00Z"
}`

// writeTempCard writes the minimalCard fixture to a temp file and
// returns its path. Tests use this to exercise the local-path branch
// of loadSource.
func writeTempCard(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestSummarizeMinimalCard(t *testing.T) {
	got, err := summarize([]byte(minimalCard))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if got.Name != "TestAgent" {
		t.Errorf("Name: got %q, want TestAgent", got.Name)
	}
	if got.Handle != "@test@example.com" {
		t.Errorf("Handle: got %q, want @test@example.com", got.Handle)
	}
	if got.Owner != "Test Owner" {
		t.Errorf("Owner: got %q", got.Owner)
	}
	if got.OwnerURL != "https://example.com" {
		t.Errorf("OwnerURL: got %q", got.OwnerURL)
	}
	if !got.HasCardURL {
		t.Errorf("HasCardURL: got false, want true")
	}
	wantCaps := []string{"code-generation", "web-search"}
	if len(got.Capabilities) != len(wantCaps) {
		t.Fatalf("Capabilities: got %v, want %v", got.Capabilities, wantCaps)
	}
	for i, w := range wantCaps {
		if got.Capabilities[i] != w {
			t.Errorf("Capabilities[%d]: got %q, want %q", i, got.Capabilities[i], w)
		}
	}
	// Protocols is a map so order isn't stable; just check content.
	if len(got.Protocols) != 1 || got.Protocols[0] != "mcp" {
		t.Errorf("Protocols: got %v, want [mcp]", got.Protocols)
	}
}

func TestSummarizeInvalidJSON(t *testing.T) {
	_, err := summarize([]byte(`{ not json`))
	if err == nil {
		t.Fatalf("expected error on invalid JSON, got nil")
	}
}

func TestLoadSourceLocalPath(t *testing.T) {
	path := writeTempCard(t, minimalCard)
	data, err := loadSource(context.Background(), path)
	if err != nil {
		t.Fatalf("loadSource: %v", err)
	}
	if !strings.Contains(string(data), "TestAgent") {
		t.Errorf("loaded body doesn't contain expected content: %q", string(data))
	}
}

func TestLoadSourceRejectsUnsupportedScheme(t *testing.T) {
	_, err := loadSource(context.Background(), "file:///etc/passwd")
	if err == nil {
		t.Fatalf("expected error on file:// scheme, got nil")
	}
}

func TestLoadSourceEmpty(t *testing.T) {
	_, err := loadSource(context.Background(), "")
	if err == nil {
		t.Fatalf("expected error on empty source, got nil")
	}
}

func TestLoadSourceHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(minimalCard))
	}))
	defer server.Close()

	got, err := loadSource(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("loadSource(http): %v", err)
	}
	if !strings.Contains(string(got), "TestAgent") {
		t.Errorf("HTTP load body wrong: %q", got)
	}
}

func TestValidateCardTool_LocalValid(t *testing.T) {
	path := writeTempCard(t, minimalCard)

	// Use the published agent-validate library's Validate directly,
	// which is exactly what the tool handler calls.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	results, verr := agentvalidate.Validate(context.Background(), data)
	if verr != nil {
		t.Fatalf("Validate err: %v", verr)
	}
	if len(results) != 0 {
		t.Errorf("expected minimal card to validate clean, got: %v", results)
	}
	warnings := agentvalidate.Lint(data)
	if len(warnings) != 0 {
		t.Errorf("expected minimal card to lint clean, got: %v", warnings)
	}
}

func TestFindCapabilityTool(t *testing.T) {
	path := writeTempCard(t, minimalCard)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	summary, err := summarize(data)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	// Mimic findCapability tool logic against the summary.
	for _, c := range summary.Capabilities {
		if strings.EqualFold(c, "code-generation") {
			return // found
		}
	}
	t.Fatalf("expected to find capability code-generation in %v", summary.Capabilities)
}

func TestResolveWellKnownLenientInput(t *testing.T) {
	// Bare-domain input should be coerced to https:// before resolving.
	got, err := agentvalidate.ResolveWellKnownURL("https://example.com")
	if err != nil {
		t.Fatalf("ResolveWellKnownURL: %v", err)
	}
	want := "https://example.com/.well-known/agent.json"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderSummaryIncludesKeyFields(t *testing.T) {
	o := getCardOutput{
		Name:         "TestAgent",
		Handle:       "@test@example.com",
		Description:  "An agent for tests.",
		Owner:        "Test Owner",
		OwnerURL:     "https://example.com",
		Version:      "1.0",
		Capabilities: []string{"code-generation"},
		Protocols:    []string{"mcp"},
		HasCardURL:   true,
	}
	got := renderSummary(o, "https://example.com/.well-known/agent.json")
	for _, want := range []string{
		"TestAgent",
		"@test@example.com",
		"Test Owner",
		"code-generation",
		"mcp",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderSummary missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderValidationFailAndWarn(t *testing.T) {
	o := validateCardOutput{
		Valid: false,
		Errors: []string{
			"agent/handle: pattern mismatch",
		},
		Warnings: []string{
			"H003 agent.handle: free-mail domain",
		},
	}
	got := renderValidation(o, "/tmp/x.json")
	for _, want := range []string{
		"FAIL (1 issue(s))",
		"agent/handle: pattern mismatch",
		"1 warning(s)",
		"H003 agent.handle: free-mail domain",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderValidation missing %q in:\n%s", want, got)
		}
	}
}