package installpack

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"text/template"
	"time"
)

var fixedZipModTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Render builds a deterministic local install-pack ZIP using the built-in
// layout for req.Profile.
func Render(ctx context.Context, req Request) (*Pack, error) {
	return NewRenderer().Render(ctx, req)
}

// Renderer renders deterministic Ba install packs.
type Renderer struct {
	layouts map[Profile]layout
}

// NewRenderer constructs a renderer with the built-in default profile layouts.
func NewRenderer() *Renderer {
	return &Renderer{layouts: defaultLayouts()}
}

// Render builds a deterministic local install-pack ZIP using the renderer's
// registered profile layouts.
func (r *Renderer) Render(ctx context.Context, req Request) (*Pack, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.New("install pack renderer is nil")
	}
	profile, err := normalizeProfile(req.Profile)
	if err != nil {
		return nil, err
	}
	stageDomain, err := normalizeStageDomain(req.StageDomain)
	if err != nil {
		return nil, err
	}
	actor, err := normalizeActor(req.Actor)
	if err != nil {
		return nil, err
	}
	layout, ok := r.layouts[profile]
	if !ok {
		return nil, fmt.Errorf("unsupported install pack profile")
	}
	serverName, err := MCPServerName(req.Namespace, stageDomain, actor, profile)
	if err != nil {
		return nil, err
	}
	mcpURL := "https://api." + stageDomain + "/mcp/" + actor
	metadataURL := "https://api." + stageDomain + "/.well-known/oauth-protected-resource/mcp/" + actor
	authorizationServerURL := "https://api." + stageDomain + "/.well-known/oauth-authorization-server"

	data := templateData{
		Profile:           string(profile),
		StageDomain:       stageDomain,
		Actor:             actor,
		Namespace:         strings.TrimSpace(req.Namespace),
		Account:           strings.TrimSpace(req.Account),
		PackID:            strings.TrimSpace(req.PackID),
		PackDigest:        strings.TrimSpace(req.PackDigest),
		MCPServerName:     serverName,
		MCPEndpointURL:    mcpURL,
		OAuthMetadataURL:  metadataURL,
		AuthorizationURL:  authorizationServerURL,
		AgentSoul:         normalizeContentBlock(req.AgentSoul, "No agent_soul content was supplied in this install pack."),
		AgentInstructions: normalizeContentBlock(req.AgentInstructions, "No agent_instructions content was supplied in this install pack."),
	}

	files := make([]renderedFile, 0, len(layout.files)+1)
	for _, spec := range layout.files {
		body, err := renderFile(spec, data)
		if err != nil {
			return nil, err
		}
		files = append(files, renderedFile{spec: spec, body: body})
	}

	entries := manifestEntries(profile, files)
	manifest := Manifest{
		Schema:         ManifestSchema,
		Version:        1,
		Profile:        profile,
		PackID:         strings.TrimSpace(req.PackID),
		PackDigest:     strings.TrimSpace(req.PackDigest),
		Account:        strings.TrimSpace(req.Account),
		Actor:          actor,
		Namespace:      strings.TrimSpace(req.Namespace),
		StageDomain:    stageDomain,
		MCPServerName:  serverName,
		MCPEndpointURL: mcpURL,
		OAuth: OAuthMetadata{
			Type:                         "lesser_oauth",
			ProtectedResourceMetadataURL: metadataURL,
			AuthorizationServerDiscovery: authorizationServerURL,
			BearerTokenEmbedded:          false,
			AccessLeaseEmbedded:          false,
		},
		InstallMarker: InstallMarkerMeta{
			Path:          installMarkerPath,
			Schema:        installMarkerSchema,
			MCPServerName: serverName,
		},
		MergeInstructions: layout.mergeInstructions(serverName),
		ManifestEntries:   entries,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal install pack manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')

	zipBytes, err := buildZip(manifestBytes, files)
	if err != nil {
		return nil, err
	}
	packChecksum := sha256String(zipBytes)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Pack{
		ZIPBytes:       zipBytes,
		PackChecksum:   packChecksum,
		Manifest:       manifest,
		MCPServerName:  serverName,
		MCPEndpointURL: mcpURL,
	}, nil
}

func renderFile(spec fileSpec, data templateData) ([]byte, error) {
	if spec.renderer != nil {
		return spec.renderer(data)
	}
	tmpl, err := template.New(spec.path).Option("missingkey=error").Parse(spec.template)
	if err != nil {
		return nil, fmt.Errorf("parse install pack template %s: %w", spec.path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render install pack template %s: %w", spec.path, err)
	}
	return buf.Bytes(), nil
}

func renderMCPJSON(data templateData) ([]byte, error) {
	cfg := struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}{
		MCPServers: map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}{
			data.MCPServerName: {
				Type: "http",
				URL:  data.MCPEndpointURL,
			},
		},
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render mcp config: %w", err)
	}
	return append(out, '\n'), nil
}

func renderInstallMarker(data templateData) ([]byte, error) {
	marker := struct {
		Schema         string `json:"schema"`
		Profile        string `json:"profile"`
		PackID         string `json:"pack_id,omitempty"`
		PackDigest     string `json:"pack_digest,omitempty"`
		Account        string `json:"account,omitempty"`
		Actor          string `json:"actor"`
		Namespace      string `json:"namespace,omitempty"`
		StageDomain    string `json:"stage_domain"`
		MCPServerName  string `json:"mcp_server_name"`
		MCPEndpointURL string `json:"mcp_endpoint_url"`
		OAuth          string `json:"oauth"`
		ManagedBy      string `json:"managed_by"`
	}{
		Schema:         installMarkerSchema,
		Profile:        data.Profile,
		PackID:         data.PackID,
		PackDigest:     data.PackDigest,
		Account:        data.Account,
		Actor:          data.Actor,
		Namespace:      data.Namespace,
		StageDomain:    data.StageDomain,
		MCPServerName:  data.MCPServerName,
		MCPEndpointURL: data.MCPEndpointURL,
		OAuth:          "lesser_oauth_protected_resource_metadata",
		ManagedBy:      "lesser-body/internal/installpack",
	}
	out, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render install marker: %w", err)
	}
	return append(out, '\n'), nil
}

func manifestEntries(profile Profile, files []renderedFile) []ManifestEntry {
	entries := make([]ManifestEntry, 0, len(files))
	for _, file := range files {
		entries = append(entries, ManifestEntry{
			Path:        file.spec.path,
			Role:        file.spec.role,
			Kind:        file.spec.kind,
			MediaType:   file.spec.mediaType,
			SizeBytes:   int64(len(file.body)),
			Checksum:    sha256String(file.body),
			Required:    file.spec.required,
			Description: file.spec.description,
			Provenance: EntryProvenance{
				Source:   "lesser-body/internal/installpack",
				Profile:  profile,
				Template: file.spec.templateName,
			},
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func buildZip(manifestBytes []byte, files []renderedFile) ([]byte, error) {
	ordered := make([]renderedFile, len(files))
	copy(ordered, files)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].spec.path < ordered[j].spec.path })

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeZipEntry(zw, "MANIFEST.json", manifestBytes); err != nil {
		_ = zw.Close()
		return nil, err
	}
	for _, file := range ordered {
		if err := writeZipEntry(zw, file.spec.path, file.body); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close install pack zip: %w", err)
	}
	return buf.Bytes(), nil
}

func writeZipEntry(zw *zip.Writer, path string, body []byte) error {
	header := &zip.FileHeader{Name: path, Method: zip.Store}
	header.SetMode(0o644)
	header.SetModTime(fixedZipModTime)
	w, err := zw.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create install pack zip entry: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write install pack zip entry: %w", err)
	}
	return nil
}

func normalizeProfile(profile Profile) (Profile, error) {
	normalized := Profile(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(string(profile)), "-", "_")))
	switch normalized {
	case ProfileClaudeCode, ProfileCodex:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported install pack profile")
	}
}

func normalizeStageDomain(stageDomain string) (string, error) {
	stageDomain = strings.ToLower(strings.TrimSpace(stageDomain))
	if stageDomain == "" {
		return "", fmt.Errorf("stage domain is required")
	}
	if strings.Contains(stageDomain, "://") || strings.ContainsAny(stageDomain, "/?#") || strings.HasPrefix(stageDomain, "api.") {
		return "", fmt.Errorf("stage domain must be a stage domain without scheme, path, query, fragment, or api prefix")
	}
	labels := strings.Split(stageDomain, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("stage domain must contain at least two DNS labels")
	}
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("stage domain contains an invalid DNS label")
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return "", fmt.Errorf("stage domain contains an invalid DNS label")
		}
	}
	return stageDomain, nil
}

func normalizeActor(actor string) (string, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "", fmt.Errorf("actor is required")
	}
	if strings.ContainsAny(actor, "/\\?#") || strings.Contains(actor, "..") {
		return "", fmt.Errorf("actor must be a single safe path segment")
	}
	for _, r := range actor {
		if r <= 0x20 || r == 0x7f {
			return "", fmt.Errorf("actor must be a single safe path segment")
		}
	}
	if url.PathEscape(actor) != actor {
		return "", fmt.Errorf("actor must be a single safe path segment")
	}
	return actor, nil
}

func normalizeContentBlock(content string, empty string) string {
	content = strings.TrimRight(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if strings.TrimSpace(content) == "" {
		return empty
	}
	return content
}

func sha256String(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
