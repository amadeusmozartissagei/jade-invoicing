// Package typsttest finds the Typst binary for tests, like dbtest does for
// the database (ADR 0017, D9).
package typsttest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Path returns JADE_TYPST_BIN, else apps/api/.bin/typst (put there by
// scripts/fetch-typst.sh), else typst on PATH. Without one the test is
// skipped locally but fails in CI.
func Path(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("JADE_TYPST_BIN"); p != "" {
		return p
	}
	name := "typst"
	if runtime.GOOS == "windows" {
		name = "typst.exe"
	}
	_, here, _, _ := runtime.Caller(0) // apps/api/internal/typsttest/typsttest.go
	local := filepath.Join(filepath.Dir(here), "..", "..", ".bin", name)
	if _, err := os.Stat(local); err == nil {
		return local
	}
	if p, err := exec.LookPath("typst"); err == nil {
		return p
	}
	if os.Getenv("CI") != "" {
		t.Fatal("typst not found in CI")
	}
	t.Skip("typst not found; run `sh scripts/fetch-typst.sh` in apps/api")
	return ""
}
