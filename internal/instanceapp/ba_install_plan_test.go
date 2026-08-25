package instanceapp_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/agentregistry"
	"github.com/equaltoai/lesser-body/internal/auth"
	"github.com/equaltoai/lesser-body/internal/baserver"
	"github.com/equaltoai/lesser-body/internal/installpack"
	"github.com/equaltoai/lesser-body/internal/instanceapp"
	"github.com/equaltoai/lesser-body/internal/lesserapi"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	"github.com/theory-cloud/apptheory/v4/testkit"
)

const baPlanRegistryAgentID = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestInstancePlaneMCP_BaAgentLocalInstallPlanDownloadVerifyReplay(t *testing.T) {
	const endpoint = "https://api.dev.example.com/instance/{surface}/mcp"
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv(baserver.EnvInstanceMCPEndpoint, endpoint)
	auth.ResetForTests()
	stubInstanceAuthorizationServerMetadata(t)

	grantStore := newDownloadGrantStore(t)
	content := newBaPlanContentStore("agent1", baPlanRegistryAgentID)
	registryStore := newBaPlanAgentRegistry("agent1", baPlanRegistryAgentID, "prototype-11")
	app, err := instanceapp.New("lesser-body-instance", "dev",
		instanceapp.WithDownloadGrantStore(grantStore),
		instanceapp.WithBaContentStore(content),
		instanceapp.WithBaInstanceEndpoint(endpoint),
		instanceapp.WithBaNamespace("equaltoai"),
		instanceapp.WithBaToolOptions(
			baserver.WithAgentRegistryStore(registryStore),
			baserver.WithActorBindingReader(&baPlanActorBindingReader{agentID: baPlanRegistryAgentID, actorUsername: "prototype-11"}),
			baserver.WithSoulBindingIntegrationBearer("binding-secret"),
			baserver.WithRateLimiter(baserver.NewInMemoryGrantMintLimiter(10, time.Minute)),
		),
	)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()
	token := newOperatorTestTokenWithAudience(t, "test-secret", "agent1", []string{"write"}, audienceForPath("/instance/ba/mcp"))
	headers := initializedMCPHeaders(t, env, app, "/instance/ba/mcp", token)

	out := callMCPTool(t, env, app, "/instance/ba/mcp", headers, baserver.ToolAgentLocalInstallPlan, map[string]any{
		"agent_id":       baPlanRegistryAgentID,
		"client":         "codex",
		"actor_username": "agent1",
	})
	if out.Result == nil || out.Result.IsError {
		t.Fatalf("plan result = %+v error=%+v", out.Result, out.Error)
	}
	data := toolResultData(t, out.Result)
	if data["schema"] != "lesserbody.agent_local_install_plan.v1" || data["grant_id"] == "" || data["download_url"] == "" {
		t.Fatalf("plan missing identity/download fields: %+v", data)
	}
	if data["mcp_endpoint_url"] != "https://api.dev.example.com/mcp/prototype-11" || data["mcp_server_name"] == "" {
		t.Fatalf("plan mcp fields = %+v", data)
	}
	packChecksum, _ := data["pack_checksum"].(string)
	if !strings.HasPrefix(packChecksum, "sha256:") || data["pack_digest"] == "" {
		t.Fatalf("plan checksum/digest fields = %+v", data)
	}
	if entries, ok := data["manifest_entries"].([]any); !ok || len(entries) == 0 {
		t.Fatalf("manifest_entries = %#v", data["manifest_entries"])
	}
	text := out.Result.Content[0].Text
	for _, forbidden := range []string{"raw-plan-token", "draft soul content", "follow the operator"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("plan text leaked %q: %s", forbidden, text)
		}
	}

	downloadURL, _ := data["download_url"].(string)
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		t.Fatalf("download URL parse: %v", err)
	}
	if parsed.Path == "" || parsed.Query().Get("token") == "" {
		t.Fatalf("download URL missing header-free token path/query: %s", downloadURL)
	}
	if parsed.Query().Get("pack_digest") != data["pack_digest"] || parsed.Query().Get("pack_id") != data["pack_id"] {
		t.Fatalf("download URL binding query mismatch: %s", downloadURL)
	}

	download := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   parsed.Path,
		Query:  parsed.Query(),
	})
	if download.Status != 200 {
		t.Fatalf("download status = %d, body=%s", download.Status, string(download.Body))
	}
	if got := firstHeader(download.Headers, "content-type"); got != "application/zip" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := checksumHex(download.Body); got != packChecksum {
		t.Fatalf("download checksum = %q, want %q", got, packChecksum)
	}
	manifest := verifyDownloadedInstallPack(t, download.Body, data)
	if manifest.MCPServerName != data["mcp_server_name"] || manifest.MCPEndpointURL != data["mcp_endpoint_url"] || manifest.PackDigest != data["pack_digest"] {
		t.Fatalf("manifest mismatch = %+v data=%+v", manifest, data)
	}

	replay := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   parsed.Path,
		Query:  parsed.Query(),
	})
	if replay.Status != 410 {
		t.Fatalf("replay status = %d, want 410; body=%s", replay.Status, string(replay.Body))
	}
}

type baPlanActorBindingReader struct {
	agentID       string
	actorUsername string
}

func (r *baPlanActorBindingReader) GetSoulBinding(_ context.Context, _ string, _ string, _ string) (*lesserapi.SoulBindingResponse, error) {
	return &lesserapi.SoulBindingResponse{
		Status:       "bound",
		BindingState: "bound",
		Agent: lesserapi.SoulBindingAgent{
			AgentID: r.agentID,
		},
		Binding: lesserapi.SoulAgentBinding{
			AgentUsername: r.actorUsername,
		},
	}, nil
}

func verifyDownloadedInstallPack(t testing.TB, zipBytes []byte, plan map[string]any) installpack.Manifest {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("read downloaded zip: %v", err)
	}
	files := map[string][]byte{}
	for _, file := range zr.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", file.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			_ = rc.Close()
			t.Fatalf("read zip entry %s: %v", file.Name, err)
		}
		if err := rc.Close(); err != nil {
			t.Fatalf("close zip entry %s: %v", file.Name, err)
		}
		files[file.Name] = buf.Bytes()
	}
	var manifest installpack.Manifest
	if err := json.Unmarshal(files["MANIFEST.json"], &manifest); err != nil {
		t.Fatalf("decode MANIFEST.json: %v\n%s", err, string(files["MANIFEST.json"]))
	}
	if manifest.PackID != plan["pack_id"] || manifest.PackDigest != plan["pack_digest"] {
		t.Fatalf("manifest pack fields = %q/%q plan=%q/%q", manifest.PackID, manifest.PackDigest, plan["pack_id"], plan["pack_digest"])
	}
	endpoint, _ := plan["mcp_endpoint_url"].(string)
	if manifest.Actor != "prototype-11" ||
		manifest.MCPEndpointURL != endpoint ||
		manifest.OAuth.ProtectedResourceMetadataURL != "https://api.dev.example.com/.well-known/oauth-protected-resource/mcp/prototype-11" ||
		strings.Contains(manifest.MCPEndpointURL, "/mcp/0x") {
		t.Fatalf(
			"manifest actor/endpoint/oauth = %q/%q/%q, want AS-compatible local actor",
			manifest.Actor,
			manifest.MCPEndpointURL,
			manifest.OAuth.ProtectedResourceMetadataURL,
		)
	}
	for _, path := range []string{".mcp.json", ".codex/config.toml"} {
		body := string(files[path])
		if !strings.Contains(body, endpoint) || strings.Contains(body, "/mcp/0x") {
			t.Fatalf("%s endpoint did not use registry local_id: %s", path, body)
		}
	}
	for _, entry := range manifest.ManifestEntries {
		body, ok := files[entry.Path]
		if !ok {
			t.Fatalf("manifest entry %s missing from ZIP", entry.Path)
		}
		if got := checksumHex(body); got != entry.Checksum {
			t.Fatalf("entry %s checksum = %q, want %q", entry.Path, got, entry.Checksum)
		}
	}
	return manifest
}

func checksumHex(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type baPlanContentStore struct {
	records map[string]*agentcontent.Record
}

type baPlanAgentRegistry struct {
	records map[string]*agentregistry.Agent
}

func newBaPlanAgentRegistry(account, agentID, localID string) *baPlanAgentRegistry {
	return &baPlanAgentRegistry{records: map[string]*agentregistry.Agent{
		strings.ToLower(strings.TrimSpace(account)) + "\x00" + strings.TrimSpace(agentID): {
			Account: strings.ToLower(strings.TrimSpace(account)),
			AgentID: strings.TrimSpace(agentID),
			LocalID: strings.TrimSpace(localID),
		},
	}}
}

func (s *baPlanAgentRegistry) Get(_ context.Context, account string, agentID string) (*agentregistry.Agent, error) {
	record := s.records[strings.ToLower(strings.TrimSpace(account))+"\x00"+strings.TrimSpace(agentID)]
	if record == nil {
		return nil, agentregistry.ErrAgentNotFound
	}
	clone := *record
	return &clone, nil
}

func newBaPlanContentStore(account, agentID string) *baPlanContentStore {
	createdAt := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC)
	return &baPlanContentStore{records: map[string]*agentcontent.Record{
		baPlanContentKey(account, agentID, agentcontent.ContentTypeAgentSoul): {
			Account:            account,
			AgentID:            agentID,
			Type:               agentcontent.ContentTypeAgentSoul,
			Content:            "published soul content",
			Version:            2,
			SoulVersion:        1,
			LifecycleState:     agentcontent.LifecycleStatePublished,
			CreatedAt:          createdAt,
			UpdatedAt:          updatedAt,
			UpdatedBySubjectID: "subject-agent1",
			Document: &agentcontent.SoulDocument{
				SchemaVersion:      agentcontent.SoulDocumentSchemaVersion,
				AgentID:            agentID,
				Body:               "published soul content",
				SoulVersion:        1,
				LifecycleState:     agentcontent.LifecycleStatePublished,
				UpdatedBySubjectID: "subject-agent1",
				CreatedAt:          createdAt.Format(time.RFC3339Nano),
				UpdatedAt:          updatedAt.Format(time.RFC3339Nano),
				Version:            2,
			},
		},
		baPlanContentKey(account, agentID, agentcontent.ContentTypeAgentInstructions): {
			Account:            account,
			AgentID:            agentID,
			Type:               agentcontent.ContentTypeAgentInstructions,
			Content:            "follow the operator",
			Version:            2,
			LifecycleState:     agentcontent.LifecycleStateDraft,
			CreatedAt:          time.Date(2026, 7, 15, 12, 1, 0, 0, time.UTC),
			UpdatedAt:          time.Date(2026, 7, 15, 12, 31, 0, 0, time.UTC),
			UpdatedBySubjectID: "subject-agent1",
		},
	}}
}

func (s *baPlanContentStore) Get(_ context.Context, account string, agentID string, contentType agentcontent.ContentType) (*agentcontent.Record, error) {
	record := s.records[baPlanContentKey(account, agentID, contentType)]
	if record == nil {
		return nil, agentcontent.ErrContentNotFound
	}
	clone := *record
	return &clone, nil
}

func baPlanContentKey(account, agentID string, contentType agentcontent.ContentType) string {
	return strings.ToLower(strings.TrimSpace(account)) + "\x00" + strings.TrimSpace(agentID) + "\x00" + string(contentType)
}
