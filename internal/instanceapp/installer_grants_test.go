package instanceapp_test

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-body/internal/downloadgrant"
	"github.com/equaltoai/lesser-body/internal/instanceapp"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	"github.com/theory-cloud/apptheory/v2/testkit"
	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
	"github.com/theory-cloud/tabletheory/v2/pkg/testing/fakedb"
)

func TestInstallerGrantDownload_HeaderFreeConsumeReturnsZipAndReplayGone(t *testing.T) {
	store := newDownloadGrantStore(t)
	binding := installerGrantTestBinding()
	issued, err := store.Issue(context.Background(), downloadgrant.IssueInput{Binding: binding})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	zipBytes := fakeZipBytes(t)
	provider := &fakeInstallerPackProvider{pack: &instanceapp.InstallerPack{ZIPBytes: zipBytes}}
	app, err := instanceapp.New("lesser-body-instance", "dev",
		instanceapp.WithDownloadGrantStore(store),
		instanceapp.WithInstallerPackProvider(provider),
	)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	env := testkit.New()

	resp := invokeInstallerGrantDownload(t, env, app, issued.GrantID, installerGrantQuery(issued.Token, issued.Binding), nil)
	if resp.Status != 200 {
		t.Fatalf("status = %d, want 200; body=%s", resp.Status, string(resp.Body))
	}
	if got := firstHeader(resp.Headers, "cache-control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := firstHeader(resp.Headers, "content-type"); got != "application/zip" {
		t.Fatalf("Content-Type = %q, want application/zip", got)
	}
	disposition := firstHeader(resp.Headers, "content-disposition")
	if !strings.HasPrefix(disposition, `attachment; filename="`) || !strings.HasSuffix(disposition, `.zip"`) {
		t.Fatalf("Content-Disposition = %q, want safe attachment zip filename", disposition)
	}
	filename := strings.TrimPrefix(disposition, `attachment; filename="`)
	filename = strings.TrimSuffix(filename, `"`)
	for _, unsafe := range []string{"/", "\\", "\n", "\r", ";", issued.Token, "sha256:"} {
		if strings.Contains(filename, unsafe) {
			t.Fatalf("Content-Disposition filename %q contains unsafe/token material %q", filename, unsafe)
		}
	}
	if !resp.IsBase64 {
		t.Fatalf("IsBase64 = false, want binary Lambda response")
	}
	if !bytes.Equal(resp.Body, zipBytes) {
		t.Fatalf("body bytes mismatch: got %d bytes want %d", len(resp.Body), len(zipBytes))
	}
	if len(resp.Body) >= 6*1024*1024 {
		t.Fatalf("body length = %d, want under 6 MB Lambda payload ceiling", len(resp.Body))
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if provider.requests[0].GrantID != issued.GrantID || provider.requests[0].Binding != issued.Binding {
		t.Fatalf("provider request = %+v, want grantID %q binding %+v", provider.requests[0], issued.GrantID, issued.Binding)
	}

	replay := invokeInstallerGrantDownload(t, env, app, issued.GrantID, installerGrantQuery(issued.Token, issued.Binding), nil)
	if replay.Status != 410 {
		t.Fatalf("replay status = %d, want 410; body=%s", replay.Status, string(replay.Body))
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls after replay = %d, want still 1", provider.calls)
	}
	assertNoTokenMaterial(t, replay.Body, issued.Token)
}

func TestInstallerGrantDownload_FailedConsumesReturn404WithoutRendering(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, store *downloadgrant.Store) (grantID string, token string, binding downloadgrant.Binding, rawSecrets []string)
	}{
		{
			name: "unknown grant",
			setup: func(t *testing.T, store *downloadgrant.Store) (string, string, downloadgrant.Binding, []string) {
				t.Helper()
				issued := issueInstallerGrant(t, store, downloadgrant.DefaultTTL)
				return "dg_missing", issued.Token, issued.Binding, []string{issued.Token}
			},
		},
		{
			name: "expired grant",
			setup: func(t *testing.T, store *downloadgrant.Store) (string, string, downloadgrant.Binding, []string) {
				t.Helper()
				issued := issueInstallerGrant(t, store, time.Millisecond)
				time.Sleep(5 * time.Millisecond)
				return issued.GrantID, issued.Token, issued.Binding, []string{issued.Token}
			},
		},
		{
			name: "token mismatch",
			setup: func(t *testing.T, store *downloadgrant.Store) (string, string, downloadgrant.Binding, []string) {
				t.Helper()
				issued := issueInstallerGrant(t, store, downloadgrant.DefaultTTL)
				wrongToken := issued.Token + "x"
				return issued.GrantID, wrongToken, issued.Binding, []string{issued.Token, wrongToken}
			},
		},
		{
			name: "binding mismatch",
			setup: func(t *testing.T, store *downloadgrant.Store) (string, string, downloadgrant.Binding, []string) {
				t.Helper()
				issued := issueInstallerGrant(t, store, downloadgrant.DefaultTTL)
				binding := issued.Binding
				binding.Profile = "live"
				return issued.GrantID, issued.Token, binding, []string{issued.Token}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newDownloadGrantStore(t)
			grantID, token, binding, rawSecrets := tc.setup(t, store)
			provider := &fakeInstallerPackProvider{pack: &instanceapp.InstallerPack{ZIPBytes: fakeZipBytes(t)}}
			app, err := instanceapp.New("lesser-body-instance", "dev",
				instanceapp.WithDownloadGrantStore(store),
				instanceapp.WithInstallerPackProvider(provider),
			)
			if err != nil {
				t.Fatalf("new app: %v", err)
			}

			resp := invokeInstallerGrantDownload(t, testkit.New(), app, grantID, installerGrantQuery(token, binding), nil)
			if resp.Status != 404 {
				t.Fatalf("status = %d, want 404; body=%s", resp.Status, string(resp.Body))
			}
			if provider.calls != 0 {
				t.Fatalf("provider calls = %d, want 0", provider.calls)
			}
			if got := firstHeader(resp.Headers, "cache-control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			for _, secret := range rawSecrets {
				assertNoTokenMaterial(t, resp.Body, secret)
			}
		})
	}
}

func TestInstallerGrantDownload_OversizePackFailsBelowLambdaCeiling(t *testing.T) {
	store := newDownloadGrantStore(t)
	issued := issueInstallerGrant(t, store, downloadgrant.DefaultTTL)
	provider := &fakeInstallerPackProvider{pack: &instanceapp.InstallerPack{ZIPBytes: bytes.Repeat([]byte{'x'}, 6*1024*1024)}}
	app, err := instanceapp.New("lesser-body-instance", "dev",
		instanceapp.WithDownloadGrantStore(store),
		instanceapp.WithInstallerPackProvider(provider),
	)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	resp := invokeInstallerGrantDownload(t, testkit.New(), app, issued.GrantID, installerGrantQuery(issued.Token, issued.Binding), nil)
	if resp.Status != 500 {
		t.Fatalf("status = %d, want 500; body=%s", resp.Status, string(resp.Body))
	}
	if len(resp.Body) >= 6*1024*1024 {
		t.Fatalf("error body length = %d, want under Lambda ceiling", len(resp.Body))
	}
	assertNoTokenMaterial(t, resp.Body, issued.Token)
}

type fakeInstallerPackProvider struct {
	pack     *instanceapp.InstallerPack
	calls    int
	requests []instanceapp.InstallerPackRequest
}

func (p *fakeInstallerPackProvider) BuildInstallerPack(_ context.Context, req instanceapp.InstallerPackRequest) (*instanceapp.InstallerPack, error) {
	p.calls++
	p.requests = append(p.requests, req)
	return p.pack, nil
}

func invokeInstallerGrantDownload(t testing.TB, env *testkit.Env, app *apptheory.App, grantID string, query map[string][]string, headers map[string][]string) apptheory.Response {
	t.Helper()
	return env.Invoke(context.Background(), app, apptheory.Request{
		Method:  "GET",
		Path:    "/instance/downloads/installer-grants/" + grantID,
		Query:   query,
		Headers: headers,
	})
}

func installerGrantQuery(token string, binding downloadgrant.Binding) map[string][]string {
	return map[string][]string{
		"token":       {token},
		"account":     {binding.Account},
		"actor":       {binding.Actor},
		"namespace":   {binding.Namespace},
		"client":      {binding.Client},
		"profile":     {binding.Profile},
		"pack_id":     {binding.PackID},
		"pack_digest": {binding.PackDigest},
	}
}

func issueInstallerGrant(t testing.TB, store *downloadgrant.Store, ttl time.Duration) *downloadgrant.IssuedGrant {
	t.Helper()
	issued, err := store.Issue(context.Background(), downloadgrant.IssueInput{Binding: installerGrantTestBinding(), TTL: ttl})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	return issued
}

func installerGrantTestBinding() downloadgrant.Binding {
	return downloadgrant.Binding{
		Account:    "account-a",
		Actor:      "agent-one",
		Namespace:  "equaltoai",
		Route:      "/instance/ba/mcp",
		Client:     "codex",
		Profile:    "dev",
		PackID:     "ba-install-pack/codex v1",
		PackDigest: "sha256:abc123",
	}
}

func newDownloadGrantStore(t testing.TB) *downloadgrant.Store {
	t.Helper()
	const tableName = "body-instance-grants-test"
	t.Setenv(downloadgrant.EnvInstanceGrantTable, tableName)

	fake := fakedb.New()
	db, err := tabletheory.NewWithClient(session.Config{Region: "us-east-1"}, fake)
	if err != nil {
		t.Fatalf("NewWithClient() error = %v", err)
	}
	if err := db.CreateTable(instanceDownloadGrantRecord{}); err != nil {
		t.Fatalf("CreateTable() error = %v", err)
	}
	store, err := downloadgrant.NewStore(db, tableName)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

type instanceDownloadGrantRecord struct {
	PK string `theorydb:"pk,attr:pk" json:"pk"`
	SK string `theorydb:"sk,attr:sk" json:"sk"`

	GrantCreatedAt  time.Time `theorydb:"attr:createdAt" json:"created_at"`
	GrantConsumedAt time.Time `theorydb:"attr:consumedAt" json:"consumed_at"`
	GrantID         string    `theorydb:"attr:grantId" json:"grant_id"`
	Status          string    `theorydb:"attr:status" json:"status"`
	TokenHash       string    `theorydb:"attr:tokenHash" json:"token_hash"`
	Account         string    `theorydb:"attr:account" json:"account"`
	Actor           string    `theorydb:"attr:actor" json:"actor"`
	Namespace       string    `theorydb:"attr:namespace" json:"namespace"`
	Route           string    `theorydb:"attr:route" json:"route"`
	Client          string    `theorydb:"attr:client" json:"client"`
	Profile         string    `theorydb:"attr:profile" json:"profile"`
	PackID          string    `theorydb:"attr:packId" json:"pack_id"`
	PackDigest      string    `theorydb:"attr:packDigest" json:"pack_digest"`
	ExpiresAt       int64     `theorydb:"attr:expiresAt" json:"expires_at"`
}

func (instanceDownloadGrantRecord) TableName() string {
	return "body-instance-grants-test"
}

func fakeZipBytes(t testing.TB) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("README.txt")
	if err != nil {
		t.Fatalf("create fake zip entry: %v", err)
	}
	if _, err := w.Write([]byte("fake installer pack for route tests\n")); err != nil {
		t.Fatalf("write fake zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close fake zip: %v", err)
	}
	return buf.Bytes()
}

func assertNoTokenMaterial(t testing.TB, body []byte, token string) {
	t.Helper()
	text := string(body)
	if token != "" && strings.Contains(text, token) {
		t.Fatalf("response body leaked raw token %q: %s", token, text)
	}
	if strings.Contains(text, "sha256:") {
		t.Fatalf("response body leaked token hash material: %s", text)
	}
}
