package mcpapp_test

import (
	"context"
	"os"
	"testing"

	"github.com/equaltoai/lesser-body/internal/mcpapp"
)

// TestMain installs a package-level default actor-admission override so the
// full-app owner-path tests admit deterministically without each test wiring
// its own Lesser HTTP stub. The default relationship is "owner", which keeps
// the owner path open; every test that needs a different admission decision
// (grantee, 403, unreachable, etc.) replaces this override with
// mcpapp.SetActorAdmissionForTests or installActorAccessHTTPStub.
func TestMain(m *testing.M) {
	restore := mcpapp.SetActorAdmissionForTests(defaultOwnerAdmission)
	code := m.Run()
	restore()
	os.Exit(code)
}

func defaultOwnerAdmission(_ context.Context, _ string, _ string) (string, error) {
	return "owner", nil
}
