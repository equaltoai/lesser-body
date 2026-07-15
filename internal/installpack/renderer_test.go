package installpack

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestRenderClaudeCodePack(t *testing.T) {
	pack := renderTestPack(t, ProfileClaudeCode)
	files := unzipFiles(t, pack.ZIPBytes)

	wantPaths := []string{".lesser-body/install-marker.json", ".mcp.json", "CLAUDE.md", "INSTALL.md", "MANIFEST.json"}
	if got := sortedKeys(files); !equalStringSlices(got, wantPaths) {
		t.Fatalf("zip paths = %v, want %v", got, wantPaths)
	}

	mcp := decodeMCPConfig(t, files[".mcp.json"])
	server := mcp.MCPServers[pack.MCPServerName]
	if server.Type != "http" {
		t.Fatalf("mcp server type = %q, want http", server.Type)
	}
	if server.URL != "https://api.dev.example.com/mcp/Arch" {
		t.Fatalf("mcp server url = %q", server.URL)
	}
	assertNoStaticCredentials(t, files[".mcp.json"])

	claude := string(files["CLAUDE.md"])
	for _, want := range []string{"draft soul text", "follow the operator", "https://api.dev.example.com/.well-known/oauth-protected-resource/mcp/Arch"} {
		if !strings.Contains(claude, want) {
			t.Fatalf("CLAUDE.md missing %q:\n%s", want, claude)
		}
	}

	manifest := decodeManifest(t, files["MANIFEST.json"])
	if manifest.Schema != ManifestSchema || manifest.Profile != ProfileClaudeCode {
		t.Fatalf("manifest schema/profile = %q/%q", manifest.Schema, manifest.Profile)
	}
	if manifest.MCPServerName != pack.MCPServerName || manifest.MCPEndpointURL != "https://api.dev.example.com/mcp/Arch" {
		t.Fatalf("manifest mcp fields = %+v", manifest)
	}
	if manifest.OAuth.Type != "lesser_oauth" || manifest.OAuth.BearerTokenEmbedded || manifest.OAuth.AccessLeaseEmbedded {
		t.Fatalf("manifest OAuth posture = %+v", manifest.OAuth)
	}
	verifyManifestEntries(t, files, manifest)
}

func TestRenderCodexPack(t *testing.T) {
	pack := renderTestPack(t, ProfileCodex)
	files := unzipFiles(t, pack.ZIPBytes)

	wantPaths := []string{".codex/config.toml", ".lesser-body/install-marker.json", ".mcp.json", "AGENTS.md", "INSTALL.md", "MANIFEST.json"}
	if got := sortedKeys(files); !equalStringSlices(got, wantPaths) {
		t.Fatalf("zip paths = %v, want %v", got, wantPaths)
	}

	mcp := decodeMCPConfig(t, files[".mcp.json"])
	server := mcp.MCPServers[pack.MCPServerName]
	if server.URL != "https://api.dev.example.com/mcp/Arch" {
		t.Fatalf("mcp server url = %q", server.URL)
	}
	assertNoStaticCredentials(t, files[".mcp.json"])

	codexConfig := string(files[".codex/config.toml"])
	if !strings.Contains(codexConfig, `[mcp_servers."`+pack.MCPServerName+`"]`) || !strings.Contains(codexConfig, `url = "https://api.dev.example.com/mcp/Arch"`) {
		t.Fatalf("codex config did not render expected MCP server:\n%s", codexConfig)
	}
	assertNoStaticCredentials(t, files[".codex/config.toml"])

	agents := string(files["AGENTS.md"])
	if !strings.Contains(agents, "draft soul text") || !strings.Contains(agents, "follow the operator") {
		t.Fatalf("AGENTS.md missing explicit content:\n%s", agents)
	}

	manifest := decodeManifest(t, files["MANIFEST.json"])
	if manifest.Schema != ManifestSchema || manifest.Profile != ProfileCodex {
		t.Fatalf("manifest schema/profile = %q/%q", manifest.Schema, manifest.Profile)
	}
	verifyManifestEntries(t, files, manifest)
}

func TestRenderIsDeterministic(t *testing.T) {
	req := testRequest(ProfileCodex)
	first, err := Render(context.Background(), req)
	if err != nil {
		t.Fatalf("Render(first) error = %v", err)
	}
	second, err := Render(context.Background(), req)
	if err != nil {
		t.Fatalf("Render(second) error = %v", err)
	}
	if !bytes.Equal(first.ZIPBytes, second.ZIPBytes) {
		t.Fatalf("rendered ZIP bytes are not deterministic")
	}
	if first.PackChecksum != checksum(second.ZIPBytes) || first.PackChecksum != second.PackChecksum {
		t.Fatalf("pack checksum mismatch first=%q second=%q", first.PackChecksum, second.PackChecksum)
	}
}

func TestMCPServerNameStableSafeAndCollisionResistant(t *testing.T) {
	name, err := MCPServerName("equaltoai", "dev.example.com", "Arch", ProfileCodex)
	if err != nil {
		t.Fatalf("MCPServerName() error = %v", err)
	}
	const want = "lesser-equaltoai-arch-dev-example-com-codex-7e8e5dda30f2"
	if name != want {
		t.Fatalf("MCPServerName() = %q, want %q", name, want)
	}
	if !regexp.MustCompile(`^[a-z0-9_-]+(?:-[a-f0-9]{12})$`).MatchString(name) {
		t.Fatalf("MCPServerName() not config-key safe: %q", name)
	}

	collidingSlug, err := MCPServerName("equaltoai", "dev.example.com", "Arch.", ProfileCodex)
	if err != nil {
		t.Fatalf("MCPServerName(colliding slug) error = %v", err)
	}
	if collidingSlug == name {
		t.Fatalf("different identity tuple produced same MCP server name %q", name)
	}

	otherProfile, err := MCPServerName("equaltoai", "dev.example.com", "Arch", ProfileClaudeCode)
	if err != nil {
		t.Fatalf("MCPServerName(other profile) error = %v", err)
	}
	if otherProfile == name {
		t.Fatalf("different profile produced same MCP server name %q", name)
	}
}

func TestValidationErrorsDoNotExposeSoulOrInstructionsContent(t *testing.T) {
	secretSoul := "SOUL-SECRET-CONTENT"
	secretInstructions := "INSTRUCTIONS-SECRET-CONTENT"
	_, err := Render(context.Background(), Request{
		Profile:           ProfileCodex,
		StageDomain:       "",
		Actor:             "Arch",
		AgentSoul:         secretSoul,
		AgentInstructions: secretInstructions,
	})
	if err == nil {
		t.Fatal("Render() error = nil, want validation error")
	}
	if strings.Contains(err.Error(), secretSoul) || strings.Contains(err.Error(), secretInstructions) {
		t.Fatalf("validation error leaked explicit content: %v", err)
	}
}

func TestUnsupportedProfileFailsClosed(t *testing.T) {
	_, err := Render(context.Background(), Request{Profile: "slack", StageDomain: "dev.example.com", Actor: "Arch"})
	if err == nil || !strings.Contains(err.Error(), "unsupported install pack profile") {
		t.Fatalf("Render(unsupported) error = %v", err)
	}
}

func renderTestPack(t *testing.T, profile Profile) *Pack {
	t.Helper()
	pack, err := Render(context.Background(), testRequest(profile))
	if err != nil {
		t.Fatalf("Render(%s) error = %v", profile, err)
	}
	if len(pack.ZIPBytes) == 0 || pack.PackChecksum == "" || pack.MCPServerName == "" {
		t.Fatalf("pack missing rendered metadata: %+v", pack)
	}
	return pack
}

func testRequest(profile Profile) Request {
	return Request{
		Profile:           profile,
		StageDomain:       "dev.example.com",
		Actor:             "Arch",
		Namespace:         "equaltoai",
		Account:           "operator-a",
		PackID:            "ba-install-pack/arch",
		PackDigest:        "sha256:input-plan-digest",
		AgentSoul:         "draft soul text",
		AgentInstructions: "follow the operator",
	}
}

func unzipFiles(t *testing.T, zipBytes []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	files := make(map[string][]byte, len(zr.File))
	for _, file := range zr.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip file %s: %v", file.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			_ = rc.Close()
			t.Fatalf("read zip file %s: %v", file.Name, err)
		}
		if err := rc.Close(); err != nil {
			t.Fatalf("close zip file %s: %v", file.Name, err)
		}
		files[file.Name] = buf.Bytes()
	}
	return files
}

func decodeManifest(t *testing.T, body []byte) Manifest {
	t.Helper()
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("decode MANIFEST.json: %v\n%s", err, string(body))
	}
	return manifest
}

func decodeMCPConfig(t *testing.T, body []byte) struct {
	MCPServers map[string]struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"mcpServers"`
} {
	t.Helper()
	var cfg struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decode .mcp.json: %v\n%s", err, string(body))
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("mcpServers count = %d, want 1", len(cfg.MCPServers))
	}
	return cfg
}

func verifyManifestEntries(t *testing.T, files map[string][]byte, manifest Manifest) {
	t.Helper()
	seen := map[string]bool{}
	for _, entry := range manifest.ManifestEntries {
		body, ok := files[entry.Path]
		if !ok {
			t.Fatalf("manifest entry %s missing from ZIP", entry.Path)
		}
		if entry.Role == "" || entry.Kind == "" || entry.MediaType == "" || entry.Description == "" || entry.Provenance.Source == "" {
			t.Fatalf("manifest entry lacks verification metadata: %+v", entry)
		}
		if entry.SizeBytes != int64(len(body)) {
			t.Fatalf("entry %s size = %d, want %d", entry.Path, entry.SizeBytes, len(body))
		}
		if got := checksum(body); entry.Checksum != got {
			t.Fatalf("entry %s checksum = %q, want %q", entry.Path, entry.Checksum, got)
		}
		if !entry.Required {
			t.Fatalf("entry %s Required = false, want true for default layouts", entry.Path)
		}
		seen[entry.Path] = true
	}
	for path := range files {
		if path == "MANIFEST.json" {
			continue
		}
		if !seen[path] {
			t.Fatalf("zip entry %s not described by manifest", path)
		}
	}
	if manifest.InstallMarker.Path != installMarkerPath || !seen[manifest.InstallMarker.Path] {
		t.Fatalf("install marker metadata not tied to manifest entry: %+v", manifest.InstallMarker)
	}
}

func assertNoStaticCredentials(t *testing.T, body []byte) {
	t.Helper()
	lower := strings.ToLower(string(body))
	for _, forbidden := range []string{"authorization", "bearer", "access_token", "refresh_token", "client_secret", "access lease", "access_lease"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("rendered config contains forbidden credential marker %q:\n%s", forbidden, string(body))
		}
	}
}

func checksum(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
