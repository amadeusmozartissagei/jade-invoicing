// Package invoicecalc computes an invoice's line net amounts, VAT breakdown,
// and totals. It is the one tax engine for both jurisdictions; they differ
// only in the currency's minor units and the tax rounding rule
// (docs/design/data-model.md, "Tax calculation"):
//
//  1. Each line: quantity × unit price / price base quantity, rounded to the
//     currency's minor unit, half away from zero (the platform's choice; no
//     rule found in either country).
//  2. Lines are grouped by (tax category, rate); a group's taxable amount is
//     the sum of its line net amounts.
//  3. Tax is computed once per group from the taxable amount, and rounded
//     with the jurisdiction's rule: half away from zero to the cent for DE
//     [BMF-2026 Rn. 17, BR-CO-17], the issuer's choice of down, up, or half up
//     to the yen for JP [NTA-QA-57]. Never per line.
//  4. Totals follow EN 16931 BR-CO-10 to BR-CO-16.
//
// The arithmetic is exact (shopspring/decimal); nothing is ever a float.
package invoicecalc

import (
	"errors"
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

// Rounding is how a group's tax amount is rounded to the currency's minor
// unit. The values are the ones stored in app.invoices.tax_rounding.
type Rounding string

const (
	// HalfAwayFromZero is commercial rounding, the rule for DE.
	HalfAwayFromZero Rounding = "half_away_from_zero"
	// Down, Up, and HalfUp are the issuer's choices in JP (切り捨て,
	// 切り上げ, 四捨五入). Amounts are never negative in phase 1, so down is
	// toward zero and half up is half away from zero.
	Down   Rounding = "down"
	Up     Rounding = "up"
	HalfUp Rounding = "half_up"
)

// maxIntegerDigits is the contract's and the database's limit for an amount:
// at most 16 digits before the decimal point (openapi.yaml, Amount;
// app.fits_currency).
const maxIntegerDigits = 16

var amountLimit = decimal.New(1, maxIntegerDigits)

// Line is the input for one invoice line.
type Line struct {
	Quantity          decimal.Decimal // BT-129, > 0
	UnitPrice         decimal.Decimal // BT-146, >= 0, net
	PriceBaseQuantity decimal.Decimal // BT-149, > 0
	TaxCategory       string          // BT-151
	TaxRate           decimal.Decimal // BT-152, percent
}

// Params are the jurisdiction's settings and the document-level inputs.
type Params struct {
	MinorUnits int32           // 2 for EUR, 0 for JPY
	Rounding   Rounding        // tax rounding
	Prepaid    decimal.Decimal // BT-113, >= 0
}

// Breakdown is one VAT breakdown (BG-23): one per tax category and rate.
type Breakdown struct {
	TaxCategory   string          // BT-118
	TaxRate       decimal.Decimal // BT-119
	TaxableAmount decimal.Decimal // BT-116
	TaxAmount     decimal.Decimal // BT-117
}

// Result is everything the engine computes. LineNetAmounts has one entry per
// input line, in order.
type Result struct {
	LineNetAmounts    []decimal.Decimal // BT-131
	Breakdowns        []Breakdown       // by category, then rate descending
	LineNetTotal      decimal.Decimal   // BT-106
	TaxExclusiveTotal decimal.Decimal   // BT-109
	TaxTotal          decimal.Decimal   // BT-110
	TaxInclusiveTotal decimal.Decimal   // BT-112
	AmountDue         decimal.Decimal   // BT-115
}

// TooLargeError reports an amount with more than 16 integer digits. Line is
// the index of the line whose net amount is too large, or -1 when a total
// or a breakdown is.
type TooLargeError struct {
	Line int
}

func (e *TooLargeError) Error() string {
	if e.Line < 0 {
		return fmt.Sprintf("invoicecalc: a total exceeds %d integer digits", maxIntegerDigits)
	}
	return fmt.Sprintf("invoicecalc: line %d net amount exceeds %d integer digits", e.Line, maxIntegerDigits)
}

// ErrInvalidInput reports input the engine does not accept: a quantity or
// price base quantity that is not positive, a negative price, rate, or
// prepaid amount, an unknown rounding rule, or negative minor units. The API
// rejects these before calling Calculate, so this is a programming error.
var ErrInvalidInput = errors.New("invoicecalc: invalid input")

// Calculate computes the line net amounts, the VAT breakdown, and the totals.
// An invoice with no lines has zero totals and no breakdown, which a draft
// may have.
func Calculate(lines []Line, p Params) (Result, error) {
	if err := check(lines, p); err != nil {
		return Result{}, err
	}

	type key struct {
		category string
		rate     string // decimal's canonical string, so 19 and 19.00 group together
	}
	groups := map[key]*Breakdown{}
	res := Result{LineNetAmounts: make([]decimal.Decimal, len(lines))}

	for i, l := range lines {
		// DivRound rounds the exact quotient half away from zero.
		net := l.Quantity.Mul(l.UnitPrice).DivRound(l.PriceBaseQuantity, p.MinorUnits)
		if !fits(net) {
			return Result{}, &TooLargeError{Line: i}
		}
		res.LineNetAmounts[i] = net
		res.LineNetTotal = res.LineNetTotal.Add(net)

		k := key{l.TaxCategory, l.TaxRate.String()}
		g, ok := groups[k]
		if !ok {
			g = &Breakdown{TaxCategory: l.TaxCategory, TaxRate: l.TaxRate}
			groups[k] = g
		}
		g.TaxableAmount = g.TaxableAmount.Add(net)
	}

	res.Breakdowns = make([]Breakdown, 0, len(groups))
	for _, g := range groups {
		// Shift(-2) divides by 100 exactly; Div would round at 16 decimals.
		g.TaxAmount = round(g.TaxableAmount.Mul(g.TaxRate).Shift(-2), p.MinorUnits, p.Rounding)
		res.TaxTotal = res.TaxTotal.Add(g.TaxAmount)
		res.Breakdowns = append(res.Breakdowns, *g)
	}
	sort.Slice(res.Breakdowns, func(i, j int) bool {
		a, b := res.Breakdowns[i], res.Breakdowns[j]
		if a.TaxCategory != b.TaxCategory {
			return a.TaxCategory < b.TaxCategory
		}
		return a.TaxRate.GreaterThan(b.TaxRate)
	})

	// BR-CO-13, 15, 16. Allowances, charges, and the rounding amount are 0 in
	// phase 1 (ADR 0007, D7).
	res.TaxExclusiveTotal = res.LineNetTotal
	res.TaxInclusiveTotal = res.TaxExclusiveTotal.Add(res.TaxTotal)
	res.AmountDue = res.TaxInclusiveTotal.Sub(p.Prepaid)

	for _, d := range []decimal.Decimal{res.LineNetTotal, res.TaxTotal, res.TaxInclusiveTotal, res.AmountDue} {
		if !fits(d) {
			return Result{}, &TooLargeError{Line: -1}
		}
	}
	return res, nil
}

// round rounds a non-negative exact tax amount to places decimals.
func round(d decimal.Decimal, places int32, r Rounding) decimal.Decimal {
	switch r {
	case Down:
		return d.RoundFloor(places)
	case Up:
		return d.RoundCeil(places)
	default: // HalfAwayFromZero, HalfUp; check rejected anything else
		return d.Round(places)
	}
}

func check(lines []Line, p Params) error {
	switch p.Rounding {
	case HalfAwayFromZero, Down, Up, HalfUp:
	default:
		return fmt.Errorf("%w: rounding %q", ErrInvalidInput, p.Rounding)
	}
	if p.MinorUnits < 0 || p.Prepaid.IsNegative() {
		return fmt.Errorf("%w: minor units or prepaid amount", ErrInvalidInput)
	}
	for i, l := range lines {
		if !l.Quantity.IsPositive() || !l.PriceBaseQuantity.IsPositive() ||
			l.UnitPrice.IsNegative() || l.TaxRate.IsNegative() {
			return fmt.Errorf("%w: line %d", ErrInvalidInput, i)
		}
	}
	return nil
}

func fits(d decimal.Decimal) bool { return d.Abs().LessThan(amountLimit) }

// FitsCurrency reports whether d has at most minorUnits decimals and at most
// 16 integer digits: the same rule as app.fits_currency. Amounts with more
// decimals are rejected, never rounded (ADR 0011).
func FitsCurrency(d decimal.Decimal, minorUnits int32) bool {
	return fits(d) && d.Equal(d.Truncate(minorUnits))
}

// MinorUnits returns the currency's number of decimals, as
// app.minor_units does.
func MinorUnits(currency string) (int32, bool) {
	switch currency {
	case "EUR":
		return 2, true
	case "JPY":
		return 0, true
	}
	return 0, false
}
