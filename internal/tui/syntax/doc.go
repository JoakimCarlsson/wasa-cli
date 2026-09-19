// Package syntax colourises source lines for the cockpit's diff pane. It wraps
// Chroma's lexers and styles behind a per-file Highlighter that renders each
// token with a caller-supplied base style, so a highlighted line keeps the
// diff band's background instead of Chroma's own terminal escapes.
package syntax
