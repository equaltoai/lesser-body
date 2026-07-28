package installpack

import (
	"fmt"
	"strings"
)

const (
	maxMCPServerNameBytes = 80

	// mcpServerNamePrefix is the human-facing brand for every Lesser MCP
	// server rendered into a local install pack.
	mcpServerNamePrefix = "lesser_ka"

	// mcpServerNameFallbackActor keeps the name well-formed when an actor
	// sanitizes to nothing.
	mcpServerNameFallbackActor = "agent"
)

// MCPServerName returns the deterministic, config-key-safe MCP server name for
// a rendered pack.
//
// The name is what a human reads in their MCP client config, in AGENTS.md /
// CLAUDE.md, and in the install marker, so it is short and pronounceable
// rather than a machine key:
//
//	lesser_ka_lab_verifier   // dev/lab stage
//	lesser_ka_verifier       // production stage
//
// Uniqueness is per (stage environment, actor). That is the scope that
// actually matters: the name is a config key inside one workspace, and the
// realistic multi-install case is several agents from one environment
// installed side by side. Two deliberate consequences:
//
//   - Profile does not participate. A workspace holds one server entry per
//     config file, and the profile is already expressed by which files the
//     pack renders, so a codex and a claude_code pack for the same actor
//     naming the same server is correct rather than a collision.
//   - Namespace and the full stage domain do not participate. A stage domain
//     belongs to one Lesser instance, and the environment token carries the
//     part a human needs. Installing the same actor from two different
//     instances of the same stage into one workspace would collide; that is
//     accepted in exchange for a readable name.
func MCPServerName(stageDomain, actor string) (string, error) {
	stageDomain, err := normalizeStageDomain(stageDomain)
	if err != nil {
		return "", err
	}
	actor, err = normalizeActor(actor)
	if err != nil {
		return "", err
	}

	parts := []string{mcpServerNamePrefix}
	if environment := stageEnvironmentToken(stageDomain); environment != "" {
		parts = append(parts, environment)
	}
	actorPart := safeNameComponent(actor)
	if actorPart == "" {
		actorPart = mcpServerNameFallbackActor
	}

	prefix := strings.Join(parts, "_")
	if budget := maxMCPServerNameBytes - len(prefix) - 1; len(actorPart) > budget {
		if budget <= 0 {
			return "", fmt.Errorf("mcp server name prefix leaves no room for the actor component")
		}
		actorPart = strings.Trim(actorPart[:budget], "_")
		if actorPart == "" {
			actorPart = mcpServerNameFallbackActor
		}
	}
	return prefix + "_" + actorPart, nil
}

// stageEnvironmentToken maps a stage domain's leading DNS label to the short
// environment token a human recognizes, and returns "" for production so the
// production name stays plain (lesser_ka_<actor>).
//
// Labels outside this set are treated as production. New deploy stages must be
// added here; otherwise their packs render production-style names.
func stageEnvironmentToken(stageDomain string) string {
	labels := strings.Split(stageDomain, ".")
	if len(labels) < 3 {
		// An apex stage domain carries no stage prefix to read.
		return ""
	}
	switch labels[0] {
	case "dev", "lab":
		return "lab"
	case "staging", "stage":
		return "staging"
	default:
		return ""
	}
}

// safeNameComponent reduces a component to the config-key-safe [a-z0-9_] set
// used by the underscore-separated server name.
func safeNameComponent(in string) string {
	in = strings.TrimSpace(strings.ToLower(in))
	if in == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range in {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
