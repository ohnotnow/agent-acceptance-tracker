// Package aatfiles carries the Claude Code skills and agent that go with
// this version of aat, built into the binary so `aat install-skills` can put
// them in place and `aat version` can tell when the installed copies differ.
// It lives at the repo root because go:embed only reaches files at or below
// the embedding package's directory.
package aatfiles

import "embed"

//go:embed claude
var Claude embed.FS
