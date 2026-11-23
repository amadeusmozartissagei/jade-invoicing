# 0006. PDF generation with Typst, pending a spike

- **Status:** Accepted (the spike passed on 2026-11-22; results below)
- **Date:** 2026-11-01
- **Decided by:** Hamza Pratama (owner), with research by the AI coding agent

## Context

Phase 1 needs a PDF for every issued invoice, with Japanese and German text. Phase 2 needs ZUGFeRD: a PDF/A-3 file with the invoice XML embedded. The PDF must be rendered from the same data as the XML, because in Germany the XML is the invoice (`docs/domain/germany-e-invoicing.md`).

Constraints: runs inside the Go server on Vercel (2 GB memory, 1 vCPU, 250 MB bundle, 4 Active CPU-hours per month on Hobby; ADR 0001), $0, open source.

Checked on 2026-11-01:

- **Typst** (Apache-2.0, latest 0.15.1) exports PDF/A-2, PDF/A-3 (`a-3b`, `a-3u`, `a-3a`), PDF/A-4, and PDF/UA-1, and can attach files to the output (`pdf.attach`). Its docs say PDF/A-3b gives "archivable PDF files that can contain arbitrary other related files as attachments." ([Typst PDF reference][typst-pdf], [changelog][typst-changelog])
- **ZUGFeRD / Factur-X** also needs a specific XMP metadata block (an `fx` PDF/A extension schema with document type, file name, version, and conformance level). No source was found that Typst writes this or lets a template add it.
- **`go-pdf/fpdf`**, the usual pure-Go library, was archived on GitHub on 2025-03-04 and moved to Codeberg. ([go-pdf/fpdf][fpdf]) A short search found no maintained open-source pure-Go library that documents PDF/A-3 output; the search was not exhaustive.

## Options

**A. Typst.** Templates are Typst files; the Go server writes the invoice data as JSON and runs a pinned Typst binary. One engine for the plain PDF now and for PDF/A-3 later. Needs a subprocess and a binary of some tens of megabytes inside the function, which is not yet proven on Vercel.

**B. A pure-Go library (fpdf on Codeberg, or `signintech/gopdf`).** No subprocess and the lowest risk on Vercel. Layout is drawn in Go code. PDF/A-3 would need a second tool in phase 2.

**C. HTML to PDF with headless Chromium.** Easiest layout, but Chromium is heavy for a free function, costs CPU per render, and does not produce PDF/A-3. Rejected.

## Decision

Option A, Typst, if a small spike confirms it works on Vercel within the free quotas. Until then this ADR stays Proposed, and Option B is the fallback.

Rules that hold either way:

- Invoice data reaches the template only as a JSON file that the template reads. User-supplied text (customer names, item descriptions) is never pasted into template source, so it cannot inject markup or code.
- Fonts: Noto Sans JP and Noto Sans (SIL Open Font License), shipped with the server.
- Templates and fonts live inside `apps/api` (ADR 0004).

### Spike acceptance criteria

1. A pinned Typst Linux binary, with its checksum recorded, ships with the Go server on Vercel and runs through `os/exec`.
2. It renders a synthetic Japanese invoice and a synthetic German invoice; the text can be selected and extracted from the PDF.
3. Measured: extra cold-start time, Active CPU per render, PDF size, and deployment bundle size (under 250 MB).
4. A `--pdf-standard a-3b` export with an attached XML file passes veraPDF, run locally or in CI. Whether the ZUGFeRD XMP block can be added is recorded as a finding, not a pass condition.

### Spike results (2026-11-22)

Run on a throwaway Vercel project (`jade-invoicing-pdf-spike`, fra1, Hobby), deleted afterwards. The code is kept on branch `spike/typst-vercel` under `spikes/typst-vercel/` and is never merged. Raw facts and sources: [`docs/research/2026-11-22-pdf-storage-and-typst.md`](../research/2026-11-22-pdf-storage-and-typst.md).

| Criterion | Result |
|---|---|
| 1. Pinned binary runs through `os/exec` | **Pass.** Typst v0.15.1, `typst-x86_64-unknown-linux-musl.tar.xz`, SHA-256 `a6d077d0…3118c` (GitHub's asset digest; the project publishes no checksum file). Downloaded at build time and checked against the digest. |
| 2. Japanese and German invoice render; text extractable | **Pass**, on Vercel and locally: kanji, kana, ß, and ü extract as text. Fonts: Noto Sans JP and Noto Sans, Regular and Bold (SIL OFL 1.1). |
| 3. Measurements | Render: 26 to 47 ms wall, 20 to 50 ms CPU (user + system) per document. PDF: 31 to 35 KB. Server binary with all assets embedded: 76 MB (limit 250 MB). Cold start: first `/healthz` 0.65 s, against 1.15 s for the current API's cold `/healthz`, so the larger binary adds nothing noticeable. Writing the assets to `/tmp` once per instance: 1.2 s, paid by the first render of an instance. |
| 4. PDF/A-3b with an attached XML passes veraPDF | **Pass.** `--pdf-standard a-3b` plus `pdf.attach(..., relationship: "data", mime-type: "text/xml")`: veraPDF (`verapdf/cli`, flavour 3b) reports PASS; the file carries `EmbeddedFile`, `AFRelationship /Data`, and `pdfaid` part 3, conformance B. **Finding:** Typst 0.15.1 has no way to add custom XMP, so the ZUGFeRD `fx` schema is still open for phase 2 (Typst issue #5667 proposes it). |

What the spike found about the Vercel Go preset, which the documentation does not state:

- **`includeFiles` cannot be used.** The `functions` key in `vercel.json` only matches files in an `api/` directory, and the preset's single server has none. Without it, the bundle holds only the server binary.
- **`installCommand` is ignored.** `buildCommand` runs, but replaces the preset's own `go build`, so it must end with `go build -o "$VERCEL_OUTPUT_FILE" ./cmd/api`.
- **So the assets are embedded.** `go:embed` puts the Typst binary, fonts, and templates into the server binary; on first use they are written to `/tmp` and the binary runs from there. `/tmp` allows execution.

Cost on the free tier: at about 50 ms CPU per render, the 4 Active CPU-hours of Vercel Hobby cover far more renders than a portfolio demo will see.

## Consequences

**Positive**

- One rendering path from phase 1 to phase 2, with PDF/A-3 support built in.
- Templates are readable text files, so layout changes do not touch Go code.
- The JSON-only data rule makes the security boundary simple to state and test.

**Negative, and how we handle it**

- **A subprocess and a large binary in a serverless function.** The spike measures the cost; if it fails, use Option B for phase 1.
- **The ZUGFeRD XMP block is unproven.** If Typst cannot write it, phase 2 adds a post-processing step or a different tool, decided in its own ADR.
- **Another tool to pin and update** in CI and local development.

## Follow-ups

- ~~Run the spike early in phase 1, before the PDF endpoint is built.~~ Done 2026-11-22.
- Phase 2: find a way to write the ZUGFeRD XMP block (a post-processing step, or Typst once #5667 lands), in its own ADR.
- Record the licenses of Typst (Apache-2.0) and the fonts (SIL OFL 1.1) when they are added to `apps/api`.

[typst-pdf]: https://typst.app/docs/reference/pdf/
[typst-changelog]: https://typst.app/docs/changelog/
[fpdf]: https://github.com/go-pdf/fpdf
