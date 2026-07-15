// Package agent declares the canonical, ordered set of AI coding-agent CLIs
// wasa knows about, plus each agent's capabilities across every seam that
// needs to know about it: launch detection, config-dir isolation, the
// autonomous/skip-permissions flag, and which recording integration it binds
// to. It is the single source of truth those seams read from, so supporting
// an agent is declared once instead of drifting across independent maps.
//
// agent is a leaf package: it imports nothing under internal/ and holds no
// behavior of its own, only the declarative association between an agent's
// launch executable and its capabilities. internal/record keeps the actual
// recorder implementations (hook install, transcript parsing) — this package
// only records, via RecorderTool, which recorder each agent binds to, matched
// by name against record.Recorder.Tool(). That keeps the dependency acyclic:
// record and profile/launch may each import agent, but agent never imports
// record, so record does not need to import launch or profile to look up an
// agent's declared capabilities, and vice versa.
package agent
