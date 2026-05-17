package mcpapp_test

import "testing"

func assertMCPPayloadBudget(t testing.TB, name string, gotBytes int, budgetBytes int) {
	t.Helper()
	t.Logf("%s jsonrpc_payload_bytes=%d budget_bytes=%d", name, gotBytes, budgetBytes)
	if gotBytes <= 0 {
		t.Fatalf("%s payload measurement did not capture bytes: %d", name, gotBytes)
	}
	if gotBytes > budgetBytes {
		t.Fatalf("%s JSON-RPC payload bytes %d exceed explicit budget %d; do not silently truncate fields",
			name,
			gotBytes,
			budgetBytes,
		)
	}
}

func assertMCPPayloadIncrease(t testing.TB, compactName string, compactBytes int, expandedName string, expandedBytes int) {
	t.Helper()
	t.Logf("%s jsonrpc_payload_bytes=%d; %s jsonrpc_payload_bytes=%d",
		compactName,
		compactBytes,
		expandedName,
		expandedBytes,
	)
	if compactBytes <= 0 || expandedBytes <= 0 {
		t.Fatalf("payload measurement missing bytes: %s=%d %s=%d",
			compactName,
			compactBytes,
			expandedName,
			expandedBytes,
		)
	}
	if expandedBytes <= compactBytes {
		t.Fatalf("%s should be larger than %s when opt-in raw/expanded payloads are requested: expanded=%d compact=%d",
			expandedName,
			compactName,
			expandedBytes,
			compactBytes,
		)
	}
}
