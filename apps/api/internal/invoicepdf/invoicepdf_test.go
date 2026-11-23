package invoicepdf_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/invoicepdf"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/typsttest"
)

// Tests run the real Typst binary and read the result back with pdftotext,
// so they check what a reader of the PDF sees. Without either tool they skip
// locally and fail in CI (the TEST_DATABASE_URL convention, ADR 0017 D9).

func missing(t *testing.T, msg string) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

func renderer(t *testing.T) *invoicepdf.Renderer {
	t.Helper()
	r, err := invoicepdf.New(invoicepdf.Options{TypstPath: typsttest.Path(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// text renders in and returns the PDF's text as pdftotext extracts it.
func text(t *testing.T, r *invoicepdf.Renderer, in invoicepdf.Input) string {
	t.Helper()
	pdf, err := r.Render(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return pdfText(t, pdf.Bytes)
}

func pdfText(t *testing.T, pdf []byte) string {
	t.Helper()
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		missing(t, "pdftotext (poppler) not found")
	}
	f := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(f, pdf, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-enc", "UTF-8", "-layout", f, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	return string(out)
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func date(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

// germanInvoice is a synthetic invoice with two rates; the totals are worked
// out by hand: 2 × 100.00 at 19 % and 3 × 10.50 at 7 %.
func germanInvoice() invoicepdf.Input {
	return invoicepdf.Input{
		Jurisdiction: "DE",
		DocumentType: "invoice",
		Number:       "RE-2026-00042",
		IssueDate:    date("2026-11-23"),
		IssuedAt:     time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC),
		SupplyDate:   ptr(date("2026-11-20")),
		DueDate:      ptr(date("2026-12-07")),
		Currency:     "EUR",
		MinorUnits:   2,
		Seller: invoicepdf.Party{
			Name: "Beispiel Werkstatt GmbH", AddressLines: []string{"Musterstraße 1"},
			PostCode: "10115", City: "Berlin", CountryCode: "DE", VatID: "DE000000000",
			IBAN: "DE00 0000 0000 0000 0000 00",
		},
		Buyer: invoicepdf.Party{
			Name: "Testkunde Süd AG", AddressLines: []string{"Probeweg 7"},
			PostCode: "80331", City: "München", CountryCode: "DE",
		},
		Lines: []invoicepdf.Line{
			{Name: "Wartung Heizungsanlage", Quantity: d("2"), UnitCode: "HUR", UnitPrice: d("100.00"), TaxRate: d("19"), NetAmount: d("200.00")},
			{Name: "Fachbuch Größenlehre", Quantity: d("3"), UnitCode: "C62", UnitPrice: d("10.50"), TaxRate: d("7"), NetAmount: d("31.50")},
		},
		Breakdown: []invoicepdf.TaxSubtotal{
			{TaxRate: d("19"), TaxableAmount: d("200.00"), TaxAmount: d("38.00")},
			{TaxRate: d("7"), TaxableAmount: d("31.50"), TaxAmount: d("2.21")},
		},
		TaxExclusiveTotal: d("231.50"),
		TaxTotal:          d("40.21"),
		TaxInclusiveTotal: d("271.71"),
		AmountDue:         d("271.71"),
	}
}

// japaneseInvoice is a synthetic qualified invoice with a reduced-rate line:
// 1 × 5,000 at 10 % and 4 × 1,200 at 8 %.
func japaneseInvoice() invoicepdf.Input {
	return invoicepdf.Input{
		Jurisdiction: "JP",
		DocumentType: "invoice",
		Number:       "INV-000007",
		IssueDate:    date("2026-11-23"),
		IssuedAt:     time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC),
		SupplyDate:   ptr(date("2026-11-21")),
		Currency:     "JPY",
		MinorUnits:   0,
		Seller: invoicepdf.Party{
			Name: "株式会社サンプル商事", AddressLines: []string{"千代田1-1"},
			PostCode: "100-0001", City: "東京都千代田区", CountryCode: "JP", JpRegistrationNumber: "T0000000000000",
		},
		Buyer: invoicepdf.Party{Name: "テスト食品株式会社", CountryCode: "JP"},
		Lines: []invoicepdf.Line{
			{Name: "事務用品一式", Quantity: d("1"), UnitCode: "C62", UnitPrice: d("5000"), TaxRate: d("10"), NetAmount: d("5000")},
			{Name: "緑茶ペットボトル", Quantity: d("4"), UnitCode: "C62", UnitPrice: d("1200"), TaxRate: d("8"), NetAmount: d("4800")},
		},
		Breakdown: []invoicepdf.TaxSubtotal{
			{TaxRate: d("10"), TaxableAmount: d("5000"), TaxAmount: d("500")},
			{TaxRate: d("8"), TaxableAmount: d("4800"), TaxAmount: d("384")},
		},
		TaxExclusiveTotal: d("9800"),
		TaxTotal:          d("884"),
		TaxInclusiveTotal: d("10684"),
		AmountDue:         d("10684"),
	}
}

// squeeze drops all whitespace: pdftotext's spacing depends on layout, and
// Typst adds space between CJK and Latin text.
func squeeze(s string) string { return strings.Join(strings.Fields(s), "") }

func assertContains(t *testing.T, got string, want ...string) {
	t.Helper()
	flat := squeeze(got)
	for _, w := range want {
		if !strings.Contains(flat, squeeze(w)) {
			t.Errorf("PDF text lacks %q\n--- text ---\n%s", w, got)
		}
	}
}

// Criterion 8, Germany: the Pflichtangaben of § 14 Abs. 4 UStG that the
// issue check enforces.
func TestGermanInvoiceShowsRequiredItems(t *testing.T) {
	got := text(t, renderer(t), germanInvoice())
	assertContains(t, got,
		"Rechnung",
		"Beispiel Werkstatt GmbH", "Musterstraße 1", "10115 Berlin", // supplier name and address
		"Testkunde Süd AG", "Probeweg 7", "80331 München", // recipient name and address
		"DE000000000",                                                        // VAT ID
		"23.11.2026",                                                         // issue date
		"RE-2026-00042",                                                      // number
		"Wartung Heizungsanlage", "Fachbuch Größenlehre", "2 Std.", "3 Stk.", // quantity and description
		"20.11.2026",      // date of supply
		"200,00", "31,50", // net per rate
		"19 %", "38,00", "7 %", "2,21", // rate and tax per rate
		"271,71 EUR",
	)
}

// Criterion 8, Japan: the six items of a qualified invoice.
func TestJapaneseInvoiceShowsRequiredItems(t *testing.T) {
	got := text(t, renderer(t), japaneseInvoice())
	assertContains(t, got,
		"適格請求書",
		"株式会社サンプル商事", "T0000000000000", // issuer and registration number
		"2026年11月21日",                      // transaction date
		"事務用品一式", "緑茶ペットボトル ※", "※は軽減税率対象", // details with the reduced-rate mark
		"10%対象", "5,000", "8%対象", "4,800", // total per rate
		"500", "384", // tax per rate
		"テスト食品株式会社", // recipient
		"10,684",
	)
	if strings.Contains(squeeze(got), "事務用品一式※") {
		t.Error("the 10 % line carries the reduced-rate mark")
	}
}

// Criterion 7: titles per jurisdiction and type, and the reference to the
// preceding invoice on credit notes and corrected invoices.
func TestTitlesAndPrecedingReference(t *testing.T) {
	r := renderer(t)
	cases := []struct {
		in    func() invoicepdf.Input
		typ   string
		title string
		ref   []string
	}{
		{germanInvoice, "invoice", "Rechnung", nil},
		{germanInvoice, "credit_note", "Rechnungskorrektur", []string{"RE-2026-00041", "02.11.2026"}},
		{germanInvoice, "corrected_invoice", "Rechnungsberichtigung", []string{"RE-2026-00041", "02.11.2026"}},
		{japaneseInvoice, "invoice", "適格請求書", nil},
		{japaneseInvoice, "credit_note", "適格返還請求書", []string{"INV-000006", "2026年11月2日"}},
		{japaneseInvoice, "corrected_invoice", "修正した適格請求書", []string{"INV-000006", "2026年11月2日"}},
	}
	for _, c := range cases {
		in := c.in()
		in.DocumentType = c.typ
		if c.ref != nil {
			in.Preceding = &invoicepdf.Reference{Number: c.ref[0], IssueDate: date("2026-11-02")}
		}
		t.Run(in.Jurisdiction+"/"+c.typ, func(t *testing.T) {
			got := text(t, r, in)
			// The title is the first word on the page; -layout may put the
			// seller heading on the same line.
			first := strings.Fields(got)[0]
			if first != c.title {
				t.Errorf("title = %q, want %q", first, c.title)
			}
			assertContains(t, got, c.ref...)
			if strings.Contains(got, "Gutschrift") {
				t.Error(`"Gutschrift" printed`)
			}
		})
	}
}

// Criterion 9: the same input renders to the same bytes, so a retried job
// stores the same object (ADR 0017, D2).
func TestRenderIsDeterministic(t *testing.T) {
	r := renderer(t)
	a, err := r.Render(context.Background(), germanInvoice())
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Render(context.Background(), germanInvoice())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes, b.Bytes) {
		t.Fatal("two renders of the same input differ")
	}
	if a.SHA256 != sha256.Sum256(a.Bytes) {
		t.Fatal("SHA256 does not match the bytes")
	}
	if !bytes.HasPrefix(a.Bytes, []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
	// The creation date is the issue time, not the render time; two renders
	// within one second would hide a clock-based date.
	if !bytes.Contains(a.Bytes, []byte("<xmp:CreateDate>2026-11-23T09:30:00")) {
		t.Error("creation date is not IssuedAt")
	}
	// PDF/A-3b identification (veraPDF itself runs in CI).
	for _, want := range []string{"<pdfaid:part>3</pdfaid:part>", "<pdfaid:conformance>B</pdfaid:conformance>"} {
		if !bytes.Contains(a.Bytes, []byte(want)) {
			t.Errorf("PDF lacks %s", want)
		}
	}
}

// Criterion 10: text from customers is data, never Typst markup.
func TestCustomerTextIsLiteral(t *testing.T) {
	in := germanInvoice()
	evil := `#panic("x") $x^2$ [*b*] \ <l> @ref = H`
	in.Buyer.Name = evil
	in.Lines[0].Name = `#read("/etc/passwd")`
	got := text(t, renderer(t), in)
	assertContains(t, got, `#panic("x") $x^2$ [*b*] \ <l> @ref = H`, `#read("/etc/passwd")`)
}

func TestRenderRejectsUnknownDocumentType(t *testing.T) {
	in := germanInvoice()
	in.DocumentType = "receipt"
	if _, err := renderer(t).Render(context.Background(), in); err == nil {
		t.Fatal("want an error")
	}
}

// A document with only the required fields: no notes, due date, or bank
// details.
func TestMinimalInvoiceRenders(t *testing.T) {
	in := germanInvoice()
	in.DueDate, in.Seller.IBAN = nil, ""
	got := text(t, renderer(t), in)
	assertContains(t, got, "Rechnung", "271,71 EUR")
}

// TestWriteSamples writes a German and a Japanese PDF to PDF_SAMPLE_DIR, for
// CI to check with veraPDF (criterion 9) and for people to look at. Without
// the variable it does nothing.
func TestWriteSamples(t *testing.T) {
	dir := os.Getenv("PDF_SAMPLE_DIR")
	if dir == "" {
		t.Skip("PDF_SAMPLE_DIR not set")
	}
	r := renderer(t)
	samples := map[string]invoicepdf.Input{}
	for _, typ := range []string{"invoice", "credit_note", "corrected_invoice"} {
		for j, in := range map[string]invoicepdf.Input{"de": germanInvoice(), "jp": japaneseInvoice()} {
			in.DocumentType = typ
			if typ != "invoice" {
				in.Preceding = &invoicepdf.Reference{Number: "X-000001", IssueDate: date("2026-11-02"), SupplyDate: ptr(date("2026-10-31"))}
			}
			samples[j+"-"+strings.ReplaceAll(typ, "_", "-")+".pdf"] = in
		}
	}
	for name, in := range samples {
		pdf, err := r.Render(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), pdf.Bytes, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Criterion 8, Japanese return invoice: the date of the return and the date
// of the original transaction (docs/domain/japan-qualified-invoice.md).
func TestJapaneseReturnInvoiceShowsBothDates(t *testing.T) {
	in := japaneseInvoice()
	in.DocumentType = "credit_note"
	in.Preceding = &invoicepdf.Reference{Number: "INV-000006", IssueDate: date("2026-11-02"), SupplyDate: ptr(date("2026-10-31"))}
	got := text(t, renderer(t), in)
	assertContains(t, got, "返還等の年月日 2026年11月21日", "元の取引年月日 2026年10月31日", "INV-000006")
}

// Criterion 8, Germany: a seller with a tax number and no VAT ID.
func TestGermanSellerWithTaxNumber(t *testing.T) {
	in := germanInvoice()
	in.Seller.VatID, in.Seller.TaxNumber = "", "00/000/00000"
	got := text(t, renderer(t), in)
	assertContains(t, got, "Steuernummer: 00/000/00000")
}

// A price per 100 units prints its base, so quantity × price = amount reads
// correctly: 300 × 10.50 per 100 = 31.50.
func TestPriceBaseQuantityIsPrinted(t *testing.T) {
	in := germanInvoice()
	in.Lines[1].Quantity, in.Lines[1].PriceBaseQuantity = d("300"), d("100")
	got := text(t, renderer(t), in)
	assertContains(t, got, "300 Stk. 10,50 / 100 Stk.")
}
