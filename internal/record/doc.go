// Package record captures agent sessions as checkpoint commits on the
// dedicated ref refs/wasa/checkpoints in the workspace repository, so every
// agent-produced change stays traceable to the prompt that asked for it and
// the conversation that produced it — on any clone, with nothing but git.
//
// Each checkpoint is one commit on the ref (parent = previous checkpoint)
// whose tree holds meta.json, intent.md and transcript.jsonl. Commits are
// built exclusively with git plumbing (hash-object, mktree, commit-tree,
// update-ref): the user's branches, index, working copy and reflog are never
// touched. Transcripts are redacted for common secret formats before they
// enter the object database; redaction is best-effort by design.
//
// Recording is best-effort by contract: every failure degrades to at most a
// single logged warning and, where possible, a checkpoint containing whatever
// is known with the gap noted in meta.json. It must never fail or noticeably
// slow the session it records.
package record
