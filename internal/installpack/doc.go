// Package installpack renders deterministic local installer ZIP packs for Ba.
//
// The package is pure and testable: it performs no network, cloud, grant minting,
// filesystem, or MCP tool-registration side effects. Callers supply explicit
// actor, stage-domain, profile, and optional soul/instructions content, and the
// renderer returns ZIP bytes plus verification metadata.
package installpack
