package mcpapp_test

import (
	"os"
	"strings"
	"testing"

	tablecore "github.com/theory-cloud/tabletheory/v3/pkg/core"
	tableerrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-body/internal/agentshare"
)

// TestMain installs a package-level default agent-share fixture so the full-app
// owner-path tests resolve the agent owner deterministically without each test
// wiring its own DynamoDB fake. The default owner is "owner" and no share grants
// exist, which keeps the owner path open and every non-owner caller denied.
func TestMain(m *testing.M) {
	setDefaultAgentShareOwner()
	code := m.Run()
	agentshare.ResetForTests()
	os.Exit(code)
}

// setDefaultAgentShareOwner reinstalls the package-level default agent-share
// fixture. Tests that override the factory (e.g. the composed admission test)
// call this on cleanup so later tests still see the default rather than the
// production DynamoDB factory left behind by agentshare.ResetForTests.
func setDefaultAgentShareOwner() {
	agentshare.SetDBFactoryForTests(func() (tablecore.DB, error) {
		return &fakeTableTheoryDB{firstFn: defaultAgentShareFirst}, nil
	})
}

func defaultAgentShareFirst(dest any, where map[string]any) error {
	pk, _ := where["PK"].(string)
	sk, _ := where["SK"].(string)
	if sk == "METADATA" {
		return setAdmissionFields(dest, map[string]any{
			"PK":         pk,
			"SK":         sk,
			"Username":   strings.TrimPrefix(pk, "USER#"),
			"IsAgent":    true,
			"AgentOwner": "owner",
		})
	}
	return tableerrors.ErrItemNotFound
}
