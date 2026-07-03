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
// schema-accurate against the upstream reflectt/agent-identity-kit
// spec without duplicating validation logic.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/NovaLux12/agent-validate/pkg/agentvalidate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is the agentcard-mcp release tag. Bump in lockstep with
// CHANGELOG.md and README install snippets.
const Version = "0.1.0"

// serverImpl is the MCP server identity advertised during handshake.
var serverImpl = &mcp.Implementation{
	Name:    "agentcard-mcp",
	Version: Version,
}

func main() {
	server := mcp.NewServer(serverImpl, nil)

	registerGetCardTool(server)
	registerValidateCardTool(server)
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
// returning the raw bytes. Stdin ("-") is intentionally NOT supported
// here because MCP tools are server-side; clients pass URLs.
func loadSource(ctx context.Context, target string) ([]byte, error) {
	if target == "" {
		return nil, errors.New("source is required")
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return agentvalidate.FetchURL(ctx, target, agentvalidate.FetchOptions{
			Timeout: 30 * time.Second,
		})
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
	if v, ok := doc["version"].(string); ok {
		out.Version = v
	}
	if agent, ok := doc["agent"].(map[string]any); ok {
		out.Name, _ = agent["name"].(string)
		out.Handle, _ = agent["handle"].(string)
		out.Description, _ = agent["description"].(string)
	}
	if owner, ok := doc["owner"].(map[string]any); ok {
		out.Owner, _ = owner["name"].(string)
		out.OwnerURL, _ = owner["url"].(string)
	}
	if caps, ok := doc["capabilities"].([]any); ok {
		for _, c := range caps {
			if s, ok := c.(string); ok {
				out.Capabilities = append(out.Capabilities, s)
			}
		}
	}
	if protocols, ok := doc["protocols"].(map[string]any); ok {
		// Each true/false key in protocols maps to a supported protocol
		// (e.g., mcp: true, a2a: true). Capture the truthy ones.
		// The "agent-card" key is a version string — capture separately.
		for k, v := range protocols {
			if b, ok := v.(bool); ok && b {
				out.Protocols = append(out.Protocols, k)
			}
		}
	}
	if endpoints, ok := doc["endpoints"].(map[string]any); ok {
		if cardURL, ok := endpoints["card"].(string); ok && cardURL != "" {
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
	Source      string `json:"source" jsonschema:"URL or local path of the agent.json"`
	Capability  string `json:"capability" jsonschema:"the capability tag to look up (e.g. \"code-generation\"). Case-insensitive."`
}

type findCapabilityOutput struct {
	Capability   string   `json:"capability"`
	Found        bool     `json:"found"`
	AllDeclared  []string `json:"all_declared,omitempty"`
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
			Capability:  args.Capability,
			AllDeclared: summary.Capabilities,
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