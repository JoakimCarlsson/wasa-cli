// Package markdown renders the markdown an agent writes — a checkpoint's
// intent, the prose inside a transcript message — as styled terminal text. It
// wraps Glamour behind a width-keyed renderer cache so the checkpoints browser
// can re-render on every resize without rebuilding the renderer each time.
package markdown
