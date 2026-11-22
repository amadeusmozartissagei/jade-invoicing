# PROTOTYPE: Typst on Vercel (ADR 0006 spike)

Throwaway code on branch `spike/typst-vercel`, never merged. It answers one question: does a pinned Typst binary run inside a Vercel Go function, and what does a render cost? Verdict: yes; the results are in `docs/adr/0006-pdf-generation-with-typst.md` on `main`.

- `vercel.json` sets `buildCommand`, which replaces the Go preset's own build: it runs `scripts/fetch-typst.sh` (Typst v0.15.1, checked against its SHA-256), then `go build -o "$VERCEL_OUTPUT_FILE" ./cmd/api`. The preset ignores `installCommand`, and `functions.includeFiles` only applies to an `api/` directory.
- `cmd/api/embed.go` embeds `cmd/api/assets` (the Typst binary, fonts, templates) with `go:embed` and writes them to `/tmp` on first use.
- `cmd/api/main.go` serves `/render?doc=de|jp[&std=a3b]` with timings in `X-Render-*` headers, and `/ls` lists the extracted files.
- `cmd/api/assets/templates/invoice.typ` reads the invoice only as JSON data (`sys.inputs.data`).
- Fonts: Noto Sans and Noto Sans JP (SIL OFL 1.1; licenses next to them).

Run locally on Windows: put `typst.exe` in `cmd/api/assets/bin/`, then `go run ./cmd/api` and open `http://localhost:3000/render?doc=jp`.
