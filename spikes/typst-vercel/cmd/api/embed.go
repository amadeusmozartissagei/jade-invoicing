package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PROTOTYPE: the Go preset bundles only the server binary, so the Typst
// binary, fonts, and templates are embedded and written to /tmp once per
// instance.
//
//go:embed all:assets
var assets embed.FS

var (
	extractOnce sync.Once
	extractDir  string
	extractTook time.Duration
	extractErr  error
)

func extracted() (string, error) {
	extractOnce.Do(func() {
		t0 := time.Now()
		extractDir = filepath.Join(os.TempDir(), "typst-root")
		extractErr = fs.WalkDir(assets, "assets", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			dst := filepath.Join(extractDir, p[len("assets"):])
			if d.IsDir() {
				return os.MkdirAll(dst, 0o755)
			}
			b, err := assets.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(dst, b, 0o755)
		})
		extractTook = time.Since(t0)
	})
	return extractDir, extractErr
}
