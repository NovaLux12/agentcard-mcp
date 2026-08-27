// Command agentcard-mcp runs an MCP (Model Context Protocol) server
// that lets agents query each other's agent-card.json. It exposes
// tools to fetch, validate, list capabilities, and look up skills on
// remote (or local) agent cards.
//
// Transport: stdio (standard for local MCP servers — the agent spawns
// this binary and communicates via JSON-RPC on stdin/stdout).
//
// Validation is delegated to NovaLux12/agent-validate (the same
// library that powers the `agent-validate` CLI), so this server stays
// schema-accurate against the NovaLux12/agent-identity-kit spec
// without duplicating validation logic.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/NovaLux12/agent-validate/pkg/agentvalidate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is the agentcard-mcp release tag. Bump in lockstep with
// CHANGELOG.md and README install snippets.
const Version = "0.2.0"

// serverImpl is the MCP server identity advertised during handshake.
var serverImpl = &mcp.Implementation{
	Name:    "agentcard-mcp",
	Version: Version,
}

func main() {
	server := mcp.NewServer(serverImpl, nil)

	registerGetCardTool(server)
	registerValidateCardTool(server)
	registerLintCardTool(server)
	registerListCapabilitiesTool(server)
	registerFindCapabilityTool(server)
	registerResolveWellKnownTool(server)

	// Stdio transport is the convention for local MCP servers; an
	// agent process spawns this binary and pipes JSON-RPC frames
	// over stdin/stdout. Logs go to stderr to avoid polluting the
	// JSON-RPC stream on stdout.
	log.SetOutput(os.Stderr)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Printf("agentcard-mcp server stopped: %v", err)
	}
}

// loadSource fetches a card from a URL or reads it from a local path,
// returning the raw bytes. The input shape must be unambiguous:
//   - http:// or https:// URL — fetched via agentvalidate.FetchURL
//   - Anything else — treated as a local path, read with os.ReadFile
//
// We explicitly reject paths that would race the JSON-RPC pipe or
// read from sensitive device nodes:
//   - "-"           (stdin alias; would race the MCP transport)
//   - "/dev/..."    (devices; /dev/stdin would consume the wire stream)
//   - "/proc/..."   (procfs; /proc/self/fd/0 is the same stdin race)
//   - any URL with a non-http(s) scheme (file://, ftp://, gopher://, etc.)
//
// The previous version of this function only filtered empty input
// and embedded NULs; stdin-equivalents fell through to os.ReadFile,
// which would open a file literally named "-" or block forever on
// /dev/stdin, racing the MCP SDK's reader for the JSON-RPC stream.
func loadSource(ctx context.Context, target string) ([]byte, error) {
	if target == "" {
		return nil, errors.New("source is required")
	}
	if target == "-" {
		return nil, fmt.Errorf("path %q is not supported (stdin alias; would race the JSON-RPC pipe)", target)
	}
	// Try URL parse; if it has an explicit scheme, dispatch on that.
	if u, err := url.Parse(target); err == nil && u.Scheme != "" {
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
			return agentvalidate.FetchURL(ctx, target, agentvalidate.FetchOptions{
				Timeout: 30 * time.Second,
			})
		default:
			return nil, fmt.Errorf("scheme %q is not supported (only http and https)", u.Scheme)
		}
	}
	// Path branch. Reject well-known device and proc paths.
	if strings.HasPrefix(target, "/dev/") || strings.HasPrefix(target, "/proc/") {
		return nil, fmt.Errorf("path %q is not supported (device or procfs)", target)
	}
	if strings.ContainsAny(target, "\x00") {
		return nil, fmt.Errorf("invalid path %q", target)
	}
	return os.ReadFile(target)
}

// ─────────────────────────────────────────────────────────────────────
// get_card — fetch + parse + summarize
// ─────────────────────────────────────────────────────────────────────

type getCardArgs struct {
	Source string `json:"source" jsonschema:"URL (http/https) or local file path of an agent.json to fetch"`
}

type getCardOutput struct {
	Name         string   `json:"name"`
	Handle       string   `json:"handle,omitempty"`
	Description  string   `json:"description,omitempty"`
	Owner        string   `json:"owner,omitempty"`
	OwnerURL     string   `json:"owner_url,omitempty"`
	Version      string   `json:"version,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Protocols    []string `json:"protocols,omitempty"`
	HasCardURL   bool     `json:"has_card_url"`
}

func registerGetCardTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_card",
		Description: "Fetch an agent.json from a URL or local path and return a parsed summary: name, handle, description, owner, capabilities, protocols, and skill count. Use this to discover what an agent is and what it can do before invoking a more specific tool.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args getCardArgs) (*mcp.CallToolResult, getCardOutput, error) {
		data, err := loadSource(ctx, args.Source)
		if err != nil {
			return nil, getCardOutput{}, fmt.Errorf("could not load %s: %w", args.Source, err)
		}

		out, err := summarize(data)
		if err != nil {
			return nil, getCardOutput{}, err
		}

		// Render as text content too, so callers that don't parse
		// structured output still get a human-readable summary.
		text := renderSummary(out, args.Source)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}

func summarize(data []byte) (getCardOutput, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return getCardOutput{}, fmt.Errorf("card is not valid JSON: %w", err)
	}

	out := getCardOutput{}
	if v, ok := doc["version"]; ok {
		s, isStr := v.(string)
		if !isStr {
			return out, fmt.Errorf("card.version: expected string, got %T", v)
		}
		out.Version = s
	}
	if agent, ok := doc["agent"]; ok {
		m, isMap := agent.(map[string]any)
		if !isMap {
			return out, fmt.Errorf("card.agent: expected object, got %T", agent)
		}
		if v, ok := m["name"].(string); ok {
			out.Name = v
		}
		if v, ok := m["handle"].(string); ok {
			out.Handle = v
		}
		if v, ok := m["description"].(string); ok {
			out.Description = v
		}
	}
	if owner, ok := doc["owner"]; ok && owner != nil {
		m, isMap := owner.(map[string]any)
		if !isMap {
			return out, fmt.Errorf("card.owner: expected object, got %T", owner)
		}
		if v, ok := m["name"].(string); ok {
			out.Owner = v
		}
		if v, ok := m["url"].(string); ok {
			out.OwnerURL = v
		}
	}
	if caps, ok := doc["capabilities"]; ok {
		arr, isArr := caps.([]any)
		if !isArr {
			return out, fmt.Errorf("card.capabilities: expected array, got %T", caps)
		}
		for i, c := range arr {
			s, isStr := c.(string)
			if !isStr {
				return out, fmt.Errorf("card.capabilities[%d]: expected string, got %T", i, c)
			}
			out.Capabilities = append(out.Capabilities, s)
		}
	}
	if protocols, ok := doc["protocols"]; ok {
		m, isMap := protocols.(map[string]any)
		if !isMap {
			return out, fmt.Errorf("card.protocols: expected object, got %T", protocols)
		}
		// Capture truthy boolean keys as supported protocols. The
		// "agent-card" key is a version string — capture separately.
		for k, v := range m {
			if b, ok := v.(bool); ok && b {
				out.Protocols = append(out.Protocols, k)
			}
		}
		sort.Strings(out.Protocols) // deterministic order for log diffing
	}
	if endpoints, ok := doc["endpoints"]; ok {
		m, isMap := endpoints.(map[string]any)
		if !isMap {
			return out, fmt.Errorf("card.endpoints: expected object, got %T", endpoints)
		}
		if v, ok := m["card"].(string); ok && v != "" {
			out.HasCardURL = true
		}
	}
	return out, nil
}

func renderSummary(o getCardOutput, source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s", o.Name)
	if o.Handle != "" {
		fmt.Fprintf(&b, " (%s)", o.Handle)
	}
	b.WriteString("\n\n")
	if o.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", o.Description)
	}
	fmt.Fprintf(&b, "Source: %s\n", source)
	if o.Version != "" {
		fmt.Fprintf(&b, "Spec: %s\n", o.Version)
	}
	if o.Owner != "" {
		fmt.Fprintf(&b, "Owner: %s", o.Owner)
		if o.OwnerURL != "" {
			fmt.Fprintf(&b, " <%s>", o.OwnerURL)
		}
		b.WriteString("\n")
	}
	if len(o.Capabilities) > 0 {
		fmt.Fprintf(&b, "Capabilities (%d): %s\n", len(o.Capabilities), strings.Join(o.Capabilities, ", "))
	}
	if len(o.Protocols) > 0 {
		fmt.Fprintf(&b, "Protocols: %s\n", strings.Join(o.Protocols, ", "))
	}
	if o.HasCardURL {
		b.WriteString("Card URL: declared (canonical endpoints.card set)\n")
	} else {
		b.WriteString("Card URL: missing — agent has not declared endpoints.card\n")
	}
	return b.String()
}

// ─────────────────────────────────────────────────────────────────────
// validate_card — schema + lint
// ─────────────────────────────────────────────────────────────────────

type validateCardArgs struct {
	Source string `json:"source" jsonschema:"URL or local path of the agent.json to validate"`
}

type validateCardOutput struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func registerValidateCardTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "validate_card",
		Description: "Validate an agent.json against the reflectt/agent-identity-kit v1 JSON Schema and run soft lint checks (handle format, capability tags, endpoint URLs, etc.). Returns valid=true if schema passes; warnings are always advisory.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args validateCardArgs) (*mcp.CallToolResult, validateCardOutput, error) {
		data, err := loadSource(ctx, args.Source)
		if err != nil {
			return nil, validateCardOutput{}, fmt.Errorf("could not load %s: %w", args.Source, err)
		}

		results, verr := agentvalidate.Validate(ctx, data)
		if verr != nil {
			return nil, validateCardOutput{}, fmt.Errorf("schema validation could not run: %w", verr)
		}

		warnings := agentvalidate.Lint(data)

		out := validateCardOutput{Valid: len(results) == 0}
		for _, r := range results {
			out.Errors = append(out.Errors, r.String())
		}
		for _, w := range warnings {
			out.Warnings = append(out.Warnings, w.String())
		}

		text := renderValidation(out, args.Source)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}

func renderValidation(o validateCardOutput, source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Validation for %s\n", source)
	if o.Valid {
		b.WriteString("  schema: PASS\n")
	} else {
		fmt.Fprintf(&b, "  schema: FAIL (%d issue(s))\n", len(o.Errors))
		for _, e := range o.Errors {
			fmt.Fprintf(&b, "    - %s\n", e)
		}
	}
	if len(o.Warnings) == 0 {
		b.WriteString("  lint:   clean\n")
	} else {
		fmt.Fprintf(&b, "  lint:   %d warning(s)\n", len(o.Warnings))
		for _, w := range o.Warnings {
			fmt.Fprintf(&b, "    - %s\n", w)
		}
	}
	return b.String()
}

// ─────────────────────────────────────────────────────────────────────
// list_capabilities
// ─────────────────────────────────────────────────────────────────────

type listCapabilitiesArgs struct {
	Source string `json:"source" jsonschema:"URL or local path of the agent.json"`
}

func registerListCapabilitiesTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_capabilities",
		Description: "Return just the capabilities array of an agent.json. Use this when you only need to know what an agent can do without parsing the full card.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args listCapabilitiesArgs) (*mcp.CallToolResult, struct {
		Capabilities []string `json:"capabilities"`
	}, error) {
		data, err := loadSource(ctx, args.Source)
		if err != nil {
			return nil, struct {
				Capabilities []string `json:"capabilities"`
			}{}, fmt.Errorf("could not load %s: %w", args.Source, err)
		}

		summary, err := summarize(data)
		if err != nil {
			return nil, struct {
				Capabilities []string `json:"capabilities"`
			}{}, err
		}
		text := fmt.Sprintf("Capabilities of %s:\n%s",
			sourceLabel(args.Source),
			strings.Join(summary.Capabilities, "\n"))
		return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: text}},
			}, struct {
				Capabilities []string `json:"capabilities"`
			}{Capabilities: summary.Capabilities}, nil
	})
}

// ─────────────────────────────────────────────────────────────────────
// find_capability — does this agent declare a specific capability tag?
// ─────────────────────────────────────────────────────────────────────
//
// The reflectt/agent-identity-kit v1 schema doesn't have an A2A-style
// skills[] array. Instead, agents declare what they can do through a
// flat capabilities[] array of kebab-case tags. This tool checks
// membership in that array — a cheap, capability-aware discovery query.

type findCapabilityArgs struct {
	Source     string `json:"source" jsonschema:"URL or local path of the agent.json"`
	Capability string `json:"capability" jsonschema:"the capability tag to look up (e.g. \"code-generation\"). Case-insensitive."`
}

type findCapabilityOutput struct {
	Capability  string   `json:"capability"`
	Found       bool     `json:"found"`
	AllDeclared []string `json:"all_declared,omitempty"`
}

func registerFindCapabilityTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_capability",
		Description: "Check whether an agent.json declares a specific capability tag in its capabilities[] array. Returns the tag and the full list of declared tags so the caller can see what's available. Use this for capability-aware discovery before invoking an agent.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args findCapabilityArgs) (*mcp.CallToolResult, findCapabilityOutput, error) {
		data, err := loadSource(ctx, args.Source)
		if err != nil {
			return nil, findCapabilityOutput{}, fmt.Errorf("could not load %s: %w", args.Source, err)
		}
		summary, err := summarize(data)
		if err != nil {
			return nil, findCapabilityOutput{}, err
		}
		want := strings.ToLower(args.Capability)
		out := findCapabilityOutput{
			Capability: args.Capability,
			// Defensive copy: summary.Capabilities is a fresh slice today,
			// but if a future refactor reuses the backing array we'd
			// silently alias this field.
			AllDeclared: append([]string(nil), summary.Capabilities...),
		}
		for _, c := range summary.Capabilities {
			if strings.ToLower(c) == want {
				out.Found = true
				break
			}
		}
		var text string
		if out.Found {
			text = fmt.Sprintf("Capability %q: FOUND on %s\n  (also declared: %s)",
				args.Capability, sourceLabel(args.Source), strings.Join(summary.Capabilities, ", "))
		} else {
			text = fmt.Sprintf("Capability %q: not found on %s\n  declared tags: %s",
				args.Capability, sourceLabel(args.Source), strings.Join(summary.Capabilities, ", "))
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}

// ─────────────────────────────────────────────────────────────────────
// resolve_wellknown — turn a domain into the canonical card URL
// ─────────────────────────────────────────────────────────────────────

type resolveWellKnownArgs struct {
	Domain string `json:"domain" jsonschema:"a base URL (e.g. https://example.com or just example.com)"`
}

func registerResolveWellKnownTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "resolve_wellknown",
		Description: "Resolve a base URL or domain to the canonical /.well-known/agent.json URL where the agent's card should be hosted, per the reflectt/agent-identity-kit convention. Useful before calling get_card / validate_card / find_skill when the caller knows the domain but not the exact card path.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args resolveWellKnownArgs) (*mcp.CallToolResult, struct {
		URL string `json:"url"`
	}, error) {
		// Be lenient: allow bare domains by prepending https:// if no scheme.
		input := args.Domain
		if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
			input = "https://" + input
		}
		resolved, err := agentvalidate.ResolveWellKnownURL(input)
		if err != nil {
			return nil, struct {
				URL string `json:"url"`
			}{}, err
		}
		text := fmt.Sprintf("%s -> %s", args.Domain, resolved)
		return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: text}},
			}, struct {
				URL string `json:"url"`
			}{URL: resolved}, nil
	})
}

// ─────────────────────────────────────────────────────────────────────
// lint_card — return structured lint warnings for a card
// ─────────────────────────────────────────────────────────────────────
//
// validate_card returns warnings as concatenated strings; lint_card
// returns them as structured fields (code, path, message separately)
// so callers can filter by warning code, count occurrences, or render
// warnings in a UI without re-parsing the human-readable form.
//
// The schema validation half is intentionally not run here — callers
// that need schema validation should call validate_card, or chain
// lint_card with a separate schema check. Separating the two keeps
// each tool's contract small.

type lintCardArgs struct {
	Source string `json:"source" jsonschema:"URL or local path of the agent.json"`
}

type lintWarning struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type lintCardOutput struct {
	Source      string         `json:"source"`
	Warnings    []lintWarning  `json:"warnings"`
	Codes       []string       `json:"codes"`
	CountByCode map[string]int `json:"count_by_code"`
	HasWarnings bool           `json:"has_warnings"`
}

func registerLintCardTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lint_card",
		Description: "Return soft lint warnings for an agent.json as structured fields (code, path, message separately) plus a count-by-code summary. Schema validation is NOT run here — use validate_card for that. Use this tool when you need to filter warnings by code or aggregate them across many cards.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args lintCardArgs) (*mcp.CallToolResult, lintCardOutput, error) {
		data, err := loadSource(ctx, args.Source)
		if err != nil {
			return nil, lintCardOutput{}, fmt.Errorf("could not load %s: %w", args.Source, err)
		}

		raw := agentvalidate.Lint(data)

		out := lintCardOutput{
			Source:      args.Source,
			Warnings:    make([]lintWarning, 0, len(raw)),
			Codes:       []string{},
			CountByCode: map[string]int{},
		}

		seenCodes := map[string]bool{}
		for _, w := range raw {
			out.Warnings = append(out.Warnings, lintWarning{
				Code:    w.Code,
				Path:    w.Path,
				Message: w.Message,
			})
			out.CountByCode[w.Code]++
			if !seenCodes[w.Code] {
				seenCodes[w.Code] = true
				out.Codes = append(out.Codes, w.Code)
			}
		}
		out.HasWarnings = len(out.Warnings) > 0

		text := renderLint(out)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}

func renderLint(o lintCardOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Lint for %s\n", o.Source)
	if !o.HasWarnings {
		b.WriteString("  clean (no warnings)\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  %d warning(s), %d distinct code(s)\n", len(o.Warnings), len(o.Codes))
	for _, w := range o.Warnings {
		if w.Path != "" {
			fmt.Fprintf(&b, "    - %s %s: %s\n", w.Code, w.Path, w.Message)
		} else {
			fmt.Fprintf(&b, "    - %s: %s\n", w.Code, w.Message)
		}
	}
	if len(o.Codes) > 0 {
		fmt.Fprintf(&b, "  codes: %s\n", strings.Join(o.Codes, ", "))
	}
	return b.String()
}

// sourceLabel is a tiny helper that returns a friendly label for the
// source argument, used in human-readable output.
func sourceLabel(s string) string {
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	if s == "" {
		return "(unknown source)"
	}
	return s
}
