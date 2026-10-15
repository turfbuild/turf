// Package release carries the documents that ship in every Turf release
// archive, so the CLI can print them (`turf license`) whether it was installed
// from an archive or built with `go install`.
package release

import _ "embed"

// License is the product license as shipped in the release archive: the
// Required Notice and the PolyForm Noncommercial License 1.0.0. The CLI's own
// source is MPL-2.0 (see the repository's LICENSE); the archive, this CLI
// together with turf-mcp-server and the engine, is the Larger Work under this
// license.
//
//go:embed LICENSE
var License string

// Notice is the NOTICE file: the trademark lines and third-party attributions.
//
//go:embed NOTICE
var Notice string
