# 0006. PDF generation with Typst, pending a spike

- **Status:** Proposed (becomes Accepted when the spike below passes)
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

- Run the spike early in phase 1, before the PDF endpoint is built.
- Record the licenses of Typst and the fonts when they are added.

[typst-pdf]: https://typst.app/docs/reference/pdf/
[typst-changelog]: https://typst.app/docs/changelog/
[fpdf]: https://github.com/go-pdf/fpdf
