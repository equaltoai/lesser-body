// Package downloadgrant stores short-lived Ba install/download grants.
//
// It is an internal instance-plane data layer over the body-owned
// INSTANCE_GRANT_TABLE table. Raw grant tokens are returned to callers only at
// issuance time; persisted state stores only a deterministic token hash and the
// binding fields required by later Ba redemption/install-plan steps.
package downloadgrant
