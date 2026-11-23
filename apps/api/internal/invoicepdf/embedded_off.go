//go:build !typst_embedded

package invoicepdf

// embeddedTypst is nil without the typst_embedded build tag: local runs and
// CI pass Options.TypstPath instead (ADR 0017, D9).
var embeddedTypst []byte
