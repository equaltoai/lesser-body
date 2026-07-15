package installpack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

const maxMCPServerNameBytes = 80

// MCPServerName returns a deterministic, config-key-safe MCP server name for a
// rendered pack. The name is human-readable, lowercase, and suffixed with a
// digest over the canonical identity tuple so colliding slugs remain distinct.
func MCPServerName(namespace, stageDomain, actor string, profile Profile) (string, error) {
	stageDomain, err := normalizeStageDomain(stageDomain)
	if err != nil {
		return "", err
	}
	actor, err = normalizeActor(actor)
	if err != nil {
		return "", err
	}
	profile, err = normalizeProfile(profile)
	if err != nil {
		return "", err
	}
	namespace = strings.TrimSpace(namespace)

	parts := []string{"lesser"}
	for _, part := range []string{namespace, actor, stageDomain, string(profile)} {
		if safe := safeNameComponent(part); safe != "" {
			parts = append(parts, safe)
		}
	}
	base := strings.Join(parts, "-")
	identity := strings.Join([]string{namespace, stageDomain, actor, string(profile)}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	suffix := hex.EncodeToString(sum[:])[:12]

	limit := maxMCPServerNameBytes - len(suffix) - 1
	if len(base) > limit {
		base = strings.Trim(base[:limit], "-_")
	}
	if base == "" {
		base = "lesser"
	}
	return fmt.Sprintf("%s-%s", base, suffix), nil
}

func safeNameComponent(in string) string {
	in = strings.TrimSpace(strings.ToLower(in))
	if in == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range in {
		keep := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if keep {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if r == '-' || r == '.' || unicode.IsSpace(r) {
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-_")
}
