//go:build typst_embedded

package invoicepdf

import _ "embed"

// embeddedTypst is the Linux Typst binary that scripts/fetch-typst.sh puts in
// embedded/ during the Vercel build, after checking its SHA-256. The Go
// preset bundles only the server binary, so Typst travels inside it
// (ADR 0006, spike results).
//
//go:embed embedded/typst
var embeddedTypst []byte
