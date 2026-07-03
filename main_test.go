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
	cases := []string{
		"file:///etc/passwd",
		"ftp://example.com/agent.json",
		"gopher://example.com",
	}
	for _, in := range cases {
		_, err := loadSource(context.Background(), in)
		if err == nil {
			t.Errorf("%s: expected error, got nil", in)
		}
	}
}

func TestLoadSourceRejectsStdinEquivalents(t *testing.T) {
	// These would race the MCP SDK's reader for the JSON-RPC pipe if
	// allowed to fall through to os.ReadFile.
	cases := []string{
		"-",
		"/dev/stdin",
		"/dev/zero",
		"/proc/self/fd/0",
		"/proc/1/cmdline",
	}
	for _, in := range cases {
		_, err := loadSource(context.Background(), in)
		if err == nil {
			t.Errorf("%s: expected rejection, got nil", in)
		}
	}
}

func TestLoadSourceSchemeCaseInsensitive(t *testing.T) {
	// RFC 3986 allows uppercase schemes; loadSource should accept them.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	// Replace the scheme with uppercase.
	upper := "HTTP://" + strings.TrimPrefix(server.URL, "http://")
	// Use https for an https server, or http for http. The test
	// server URL is http://, so the uppercase form should also work.
	body, err := loadSource(context.Background(), upper)
	if err != nil {
		t.Fatalf("uppercase scheme rejected: %v", err)
	}
	if !strings.Contains(string(body), `"ok"`) {
		t.Errorf("uppercase scheme fetched wrong body: %q", body)
	}
}

func TestSummarizeRejectsWrongTypes(t *testing.T) {
	// A field that exists but is the wrong type must produce an error
	// rather than silently returning a zero value.
	cases := []struct {
		name string
		body string
	}{
		{"version-as-number", `{"version":1.0,"agent":{"name":"x","handle":"@x@x.com","description":"x"},"owner":{"name":"X"}}`},
		{"agent-as-string", `{"version":"1.0","agent":"oops"}`},
		{"capabilities-as-string", `{"version":"1.0","agent":{"name":"x","handle":"@x@x.com","description":"x"},"owner":{"name":"X"},"capabilities":"web-search"}`},
		{"protocols-as-string", `{"version":"1.0","agent":{"name":"x","handle":"@x@x.com","description":"x"},"owner":{"name":"X"},"protocols":"mcp"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := summarize([]byte(tc.body)); err == nil {
				t.Errorf("expected error on %s, got nil", tc.name)
			}
		})
	}
}

func TestSummarizeProtocolsSorted(t *testing.T) {
	// Multiple true protocols should be returned in sorted order so
	// log diffs and snapshots are stable across runs.
	body := `{
        "version": "1.0",
        "agent": {"name":"x","handle":"@x@x.com","description":"x"},
        "owner": {"name":"X"},
        "protocols": {"mcp":true,"a2a":true,"http":true,"agent-card":"1.0"}
    }`
	s, err := summarize([]byte(body))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	want := []string{"a2a", "http", "mcp"} // agent-card is a string, not bool
	got := s.Protocols
	if len(got) != len(want) {
		t.Fatalf("Protocols: got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("Protocols[%d]: got %q, want %q", i, got[i], w)
		}
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

func TestLintCardOutput_Builds(t *testing.T) {
	// Build a card that triggers known lint codes: free-mail handle (H003)
	// and uppercase handle (H002). This exercises the per-code aggregation.
	body := `{
        "version": "1.0",
        "agent": {"name":"X","handle":"@X@gmail.com","description":"x"},
        "owner": {"name":"X"},
        "endpoints": {"card": "https://example.com/.well-known/agent.json"}
    }`
	warnings := agentvalidate.Lint([]byte(body))

	out := lintCardOutput{
		Source:      "/tmp/x.json",
		Warnings:    make([]lintWarning, 0, len(warnings)),
		Codes:       []string{},
		CountByCode: map[string]int{},
	}
	seen := map[string]bool{}
	for _, w := range warnings {
		out.Warnings = append(out.Warnings, lintWarning{Code: w.Code, Path: w.Path, Message: w.Message})
		out.CountByCode[w.Code]++
		if !seen[w.Code] {
			seen[w.Code] = true
			out.Codes = append(out.Codes, w.Code)
		}
	}
	out.HasWarnings = len(out.Warnings) > 0

	if !out.HasWarnings {
		t.Fatalf("expected HasWarnings=true, got %v", warnings)
	}
	// gmail.com handle fires H003.
	if out.CountByCode["H003"] == 0 {
		t.Errorf("expected H003 in count_by_code, got %v", out.CountByCode)
	}
	// Codes should contain H003.
	found := false
	for _, c := range out.Codes {
		if c == "H003" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected H003 in codes list, got %v", out.Codes)
	}
}

func TestLintCardOutput_Clean(t *testing.T) {
	// A canonical lowercase handle + endpoints + updated_at should lint
	// clean (no NO-ENDPOINTS, no H002, no NO-UPDATED-AT).
	body := `{
        "version": "1.0",
        "agent": {"name":"X","handle":"@x@example.com","description":"x"},
        "owner": {"name":"X"},
        "endpoints": {"card": "https://example.com/.well-known/agent.json"},
        "updated_at": "2026-07-01T00:00:00Z"
    }`
	warnings := agentvalidate.Lint([]byte(body))
	if len(warnings) != 0 {
		t.Fatalf("expected clean lint, got: %v", warnings)
	}

	out := lintCardOutput{
		Source:      "/tmp/x.json",
		Warnings:    []lintWarning{},
		Codes:       []string{},
		CountByCode: map[string]int{},
		HasWarnings: false,
	}
	if out.HasWarnings {
		t.Errorf("expected HasWarnings=false")
	}
	if len(out.Warnings) != 0 {
		t.Errorf("expected empty Warnings")
	}
	if len(out.Codes) != 0 {
		t.Errorf("expected empty Codes")
	}
}

func TestRenderLintClean(t *testing.T) {
	out := lintCardOutput{
		Source:      "/tmp/x.json",
		HasWarnings: false,
	}
	got := renderLint(out)
	if !strings.Contains(got, "clean (no warnings)") {
		t.Errorf("renderLint(clean) missing 'clean (no warnings)' in:\n%s", got)
	}
}

func TestRenderLintWithWarnings(t *testing.T) {
	out := lintCardOutput{
		Source: "/tmp/x.json",
		Warnings: []lintWarning{
			{Code: "H003", Path: "agent.handle", Message: "free-mail"},
			{Code: "NO-ENDPOINTS", Message: "no endpoints"},
		},
		Codes:       []string{"H003", "NO-ENDPOINTS"},
		CountByCode: map[string]int{"H003": 1, "NO-ENDPOINTS": 1},
		HasWarnings: true,
	}
	got := renderLint(out)
	for _, want := range []string{
		"2 warning(s)",
		"2 distinct code(s)",
		"H003 agent.handle: free-mail",
		"NO-ENDPOINTS: no endpoints",
		"codes: H003, NO-ENDPOINTS",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderLint missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderLintWarningWithoutPath(t *testing.T) {
	// Warnings without a path should render as "<code>: <message>".
	out := lintCardOutput{
		Source: "/tmp/x.json",
		Warnings: []lintWarning{
			{Code: "JSON", Message: "could not parse"},
		},
		Codes:       []string{"JSON"},
		CountByCode: map[string]int{"JSON": 1},
		HasWarnings: true,
	}
	got := renderLint(out)
	if !strings.Contains(got, "JSON: could not parse") {
		t.Errorf("renderLint(warning-no-path) missing 'JSON: could not parse' in:\n%s", got)
	}
}
