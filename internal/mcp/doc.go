// Package mcp injects a profile's declared MCP servers into a session's
// agent. It is the one place that turns registry.MCPServer declarations into
// the agent's own MCP configuration, written inside the session's worktree so
// it travels with the session and disappears with it at finish.
//
// Which file an agent reads, and under which key, is declared once in
// internal/agent; an agent that declares no MCP mechanism is skipped, never
// failed, so a workspace can declare servers and still launch every agent.
package mcp
