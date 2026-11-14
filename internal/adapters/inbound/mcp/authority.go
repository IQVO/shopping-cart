package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Authority levels (Agentic Standard v2, AS-1). Every registered tool declares
// exactly one of these in its Meta under authorityMetaKey; the fleet rule is in
// warehouse-docs agents/fleet/agent-authority.md. This context's MCP surface is
// read-only (ADR 0009), so every tool here is L1: a pure read of this context's
// own state that changes nothing. Nothing here recommends (L2) or writes
// (L3, L4), so no such level is declared: a constant nothing uses would be
// dead code, and the fleet's static authority sensor reads only the key, the
// helper and the level a tool names.
const (
	// authorityMetaKey is the tools/list _meta key carrying a tool's level.
	authorityMetaKey = "com.warehouse/authority"
	// authorityL1 reads facts and changes nothing.
	authorityL1 = "L1"
)

// authorityMeta is the Meta a tool registers to declare its authority level.
func authorityMeta(level string) mcp.Meta {
	return mcp.Meta{authorityMetaKey: level}
}
