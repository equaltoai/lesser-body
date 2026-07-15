// Package agentregistry stores Ptah-created agent registry entries scoped by
// account-holder principal.
//
// The registry is an internal instance-plane data layer. It uses the
// body-owned INSTANCE_REGISTRY_TABLE table provisioned for Ptah/Ba instance
// state, not the Lesser-owned LESSER_TABLE_NAME table. Records are keyed by the
// account-holder scope and the created agent id so cross-account reads resolve
// to not-found instead of leaking existence.
package agentregistry
