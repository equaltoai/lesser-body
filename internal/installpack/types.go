package installpack

const (
	// ManifestSchema is the root MANIFEST.json schema identifier for Ba local
	// installer packs.
	ManifestSchema = "lesserbody.agent_local_install_pack.v1"

	installMarkerSchema = "lesserbody.agent_local_install_marker.v1"

	// ProfileClaudeCode renders a Claude Code local-install layout.
	ProfileClaudeCode Profile = "claude_code"
	// ProfileCodex renders a Codex local-install layout.
	ProfileCodex Profile = "codex"
)

// Profile selects the deterministic default local-install layout.
type Profile string

// Request describes a deterministic Ba install-pack render.
//
// AgentSoul and AgentInstructions are explicit caller inputs. They may be
// rendered into profile instruction files, but the renderer never includes their
// raw values in returned errors.
type Request struct {
	Profile           Profile
	StageDomain       string
	Actor             string
	Namespace         string
	Account           string
	PackID            string
	PackDigest        string
	AgentSoul         string
	AgentInstructions string
}

// Pack is the rendered ZIP and deterministic metadata useful to later
// install-plan/grant surfaces.
type Pack struct {
	ZIPBytes       []byte
	PackChecksum   string
	Manifest       Manifest
	MCPServerName  string
	MCPEndpointURL string
}

// Manifest is written as root MANIFEST.json in every pack.
type Manifest struct {
	Schema            string             `json:"schema"`
	Version           int                `json:"version"`
	Profile           Profile            `json:"profile"`
	PackID            string             `json:"pack_id,omitempty"`
	PackDigest        string             `json:"pack_digest,omitempty"`
	Account           string             `json:"account,omitempty"`
	Actor             string             `json:"actor"`
	Namespace         string             `json:"namespace,omitempty"`
	StageDomain       string             `json:"stage_domain"`
	MCPServerName     string             `json:"mcp_server_name"`
	MCPEndpointURL    string             `json:"mcp_endpoint_url"`
	OAuth             OAuthMetadata      `json:"oauth"`
	InstallMarker     InstallMarkerMeta  `json:"install_marker"`
	MergeInstructions []MergeInstruction `json:"merge_instructions"`
	ManifestEntries   []ManifestEntry    `json:"manifest_entries"`
}

// OAuthMetadata documents the OAuth posture represented by the rendered client
// config. It intentionally contains no bearer token, lease, client secret, or
// access credential.
type OAuthMetadata struct {
	Type                         string `json:"type"`
	ProtectedResourceMetadataURL string `json:"protected_resource_metadata_url"`
	AuthorizationServerDiscovery string `json:"authorization_server_discovery"`
	BearerTokenEmbedded          bool   `json:"bearer_token_embedded"`
	AccessLeaseEmbedded          bool   `json:"access_lease_embedded"`
}

// InstallMarkerMeta points to the install marker entry included in the ZIP.
type InstallMarkerMeta struct {
	Path          string `json:"path"`
	Schema        string `json:"schema"`
	MCPServerName string `json:"mcp_server_name"`
}

// MergeInstruction gives deterministic, client-local guidance. The server does
// not mutate the caller workspace.
type MergeInstruction struct {
	Path        string `json:"path"`
	Action      string `json:"action"`
	Description string `json:"description"`
}

// ManifestEntry describes one non-MANIFEST.json ZIP entry and gives enough
// metadata for local checksum verification.
type ManifestEntry struct {
	Path        string          `json:"path"`
	Role        string          `json:"role"`
	Kind        string          `json:"kind"`
	MediaType   string          `json:"media_type"`
	SizeBytes   int64           `json:"size_bytes"`
	Checksum    string          `json:"checksum"`
	Required    bool            `json:"required"`
	Description string          `json:"description"`
	Provenance  EntryProvenance `json:"provenance"`
}

// EntryProvenance identifies the deterministic renderer source for an entry.
type EntryProvenance struct {
	Source   string  `json:"source"`
	Profile  Profile `json:"profile"`
	Template string  `json:"template"`
}
