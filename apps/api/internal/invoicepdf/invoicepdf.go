// Package invoicepdf renders an issued document as a PDF/A-3b file with Typst
// (ADR 0006, ADR 0017).
//
// The caller passes plain data; the package turns it into a view model with
// every word and number already formatted for the jurisdiction, writes that
// as JSON, and runs a pinned Typst binary on one template. Text from tenants
// and customers reaches Typst only as JSON values, never as markup, so it
// cannot run code in the template.
package invoicepdf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// Input is an issued document with everything its PDF shows. Only fields that
// never change after issue belong here (ADR 0017, D3), so a retried render
// gives the same bytes.
type Input struct {
	Jurisdiction string // "DE" or "JP"
	DocumentType string // "invoice", "credit_note" or "corrected_invoice"
	Number       string
	IssueDate    time.Time
	// IssuedAt becomes the PDF's creation date, which keeps renders
	// reproducible.
	IssuedAt          time.Time
	SupplyDate        *time.Time
	SupplyPeriodStart *time.Time
	SupplyPeriodEnd   *time.Time
	DueDate           *time.Time
	BuyerReference    *string
	OrderReference    *string
	PaymentTerms      *string
	Note              *string
	Currency          string
	MinorUnits        int32
	Seller            Party
	Buyer             Party
	Lines             []Line
	Breakdown         []TaxSubtotal
	TaxExclusiveTotal decimal.Decimal
	TaxTotal          decimal.Decimal
	TaxInclusiveTotal decimal.Decimal
	PrepaidAmount     decimal.Decimal
	AmountDue         decimal.Decimal
	// Preceding is the invoice a credit note or corrected invoice refers to.
	Preceding *Reference
}

// Party is the seller or buyer as frozen on the document.
type Party struct {
	Name                 string
	AddressLines         []string
	PostCode             string
	City                 string
	CountryCode          string
	VatID                string
	TaxNumber            string
	JpRegistrationNumber string
	IBAN                 string
	BIC                  string
	AccountName          string
}

// Line is one item on the document.
type Line struct {
	Name        string
	Description string
	Quantity    decimal.Decimal
	UnitCode    string
	UnitPrice   decimal.Decimal
	// PriceBaseQuantity is how many units UnitPrice is for; zero means 1.
	PriceBaseQuantity decimal.Decimal
	TaxRate           decimal.Decimal
	NetAmount         decimal.Decimal
	PeriodStart       *time.Time
	PeriodEnd         *time.Time
}

// TaxSubtotal is the taxable amount and tax of one rate.
type TaxSubtotal struct {
	TaxRate       decimal.Decimal
	TaxableAmount decimal.Decimal
	TaxAmount     decimal.Decimal
}

// Reference names the preceding invoice.
type Reference struct {
	Number    string
	IssueDate time.Time
	// The preceding invoice's supply date or period. A Japanese return
	// invoice prints it as the date of the original transaction.
	SupplyDate        *time.Time
	SupplyPeriodStart *time.Time
	SupplyPeriodEnd   *time.Time
}

// PDF is a rendered document.
type PDF struct {
	Bytes  []byte
	SHA256 [32]byte
}

// Options configure a Renderer.
type Options struct {
	// TypstPath is the Typst binary to run. Empty means the binary embedded
	// with the typst_embedded build tag; without the tag, New fails.
	TypstPath string
	// Timeout bounds one render. Zero means 20 seconds.
	Timeout time.Duration
}

//go:embed all:assets
var assets embed.FS

// Renderer runs Typst. It is safe for concurrent use. The fonts, the
// template, and (when embedded) the binary are written to a temporary
// directory on the first render, not in New, so a cold start that never
// renders does not pay for it.
type Renderer struct {
	opts Options

	mu   sync.Mutex
	root string // the Typst project root: fonts/, templates/, work/; empty until prepared
	bin  string
}

// New returns a Renderer. It checks only that a binary is available.
func New(opts Options) (*Renderer, error) {
	if opts.TypstPath == "" && embeddedTypst == nil {
		return nil, errors.New("invoicepdf: no Typst binary: set TypstPath or build with -tags typst_embedded")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 20 * time.Second
	}
	return &Renderer{opts: opts}, nil
}

// Close removes the temporary directory. Renders must have finished.
func (r *Renderer) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.root == "" {
		return nil
	}
	err := os.RemoveAll(r.root)
	r.root, r.bin = "", ""
	return err
}

// prepare writes the assets once. A failure leaves nothing behind and is
// tried again by the next render, so one bad moment (a full /tmp) does not
// break the instance for good.
func (r *Renderer) prepare() (root, bin string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.root != "" {
		return r.root, r.bin, nil
	}
	root, err = os.MkdirTemp("", "invoicepdf-")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(root)
		}
	}()
	err = fs.WalkDir(assets, "assets", func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(p[len("assets"):]))
		if e.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		b, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		return "", "", err
	}
	if err = os.MkdirAll(filepath.Join(root, "work"), 0o700); err != nil {
		return "", "", err
	}
	bin = r.opts.TypstPath
	if bin == "" {
		bin = filepath.Join(root, "typst")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		if err = os.WriteFile(bin, embeddedTypst, 0o700); err != nil { //nolint:gosec // G306: an executable, owner only
			return "", "", err
		}
	}
	r.root, r.bin = root, bin
	return root, bin, nil
}

// Render returns the PDF/A-3b file for in.
func (r *Renderer) Render(ctx context.Context, in Input) (PDF, error) {
	v, err := buildView(in)
	if err != nil {
		return PDF{}, err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return PDF{}, err
	}
	root, bin, err := r.prepare()
	if err != nil {
		return PDF{}, fmt.Errorf("invoicepdf: prepare: %w", err)
	}

	// The data goes to a file, not to --input, because a command-line
	// argument is limited (128 KiB on Linux, 32 KiB on Windows).
	work, err := os.MkdirTemp(filepath.Join(root, "work"), "")
	if err != nil {
		return PDF{}, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	if err := os.WriteFile(filepath.Join(work, "data.json"), data, 0o600); err != nil {
		return PDF{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "compile", //nolint:gosec // G204: our pinned binary and paths; invoice data goes in a file
		"--root", root,
		"--font-path", filepath.Join(root, "fonts"),
		"--ignore-system-fonts",
		"--pdf-standard", "a-3b",
		"--creation-timestamp", strconv.FormatInt(in.IssuedAt.Unix(), 10),
		// A path inside --root, which Typst resolves from the root.
		"--input", "data="+path.Join("/work", filepath.Base(work), "data.json"),
		filepath.Join(root, "templates", "invoice.typ"),
		"-",
	)
	cmd.Env = []string{} // no TYPST_* settings from the environment
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return PDF{}, fmt.Errorf("invoicepdf: typst: %w: %s", err, firstLine(stderr.Bytes()))
	}
	b := out.Bytes()
	return PDF{Bytes: b, SHA256: sha256.Sum256(b)}, nil
}

// firstLine keeps only the first line of Typst's diagnostics, at most 200
// bytes. The full output can quote the template and, in principle, data from
// it, and errors end up in logs, which must not hold personal data.
func firstLine(b []byte) string {
	b = bytes.TrimSpace(b)
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b = b[:i]
	}
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}
