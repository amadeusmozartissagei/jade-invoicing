package invoicecalc

import (
	"errors"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func line(qty, price, rate string) Line {
	return Line{Quantity: d(qty), UnitPrice: d(price), PriceBaseQuantity: d("1"), TaxCategory: "S", TaxRate: d(rate)}
}

var (
	de = Params{MinorUnits: 2, Rounding: HalfAwayFromZero}
	jp = func(r Rounding) Params { return Params{MinorUnits: 0, Rounding: r} }
)

func mustCalc(t *testing.T, lines []Line, p Params) Result {
	t.Helper()
	res, err := Calculate(lines, p)
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	return res
}

func wantEqual(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(d(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}

// The German example from openapi.yaml: two rates, tax once per rate.
func TestGermanInvoiceWithTwoRates(t *testing.T) {
	res := mustCalc(t, []Line{line("10", "95.00", "19"), line("1", "39.90", "7")}, de)

	wantEqual(t, "line 0", res.LineNetAmounts[0], "950.00")
	wantEqual(t, "line 1", res.LineNetAmounts[1], "39.90")
	if len(res.Breakdowns) != 2 {
		t.Fatalf("breakdowns = %+v", res.Breakdowns)
	}
	// Standard rate first.
	wantEqual(t, "19% rate", res.Breakdowns[0].TaxRate, "19")
	wantEqual(t, "19% taxable", res.Breakdowns[0].TaxableAmount, "950.00")
	wantEqual(t, "19% tax", res.Breakdowns[0].TaxAmount, "180.50")
	wantEqual(t, "7% taxable", res.Breakdowns[1].TaxableAmount, "39.90")
	wantEqual(t, "7% tax", res.Breakdowns[1].TaxAmount, "2.79") // 2.793
	wantEqual(t, "line net total", res.LineNetTotal, "989.90")
	wantEqual(t, "tax exclusive", res.TaxExclusiveTotal, "989.90")
	wantEqual(t, "tax total", res.TaxTotal, "183.29")
	wantEqual(t, "tax inclusive", res.TaxInclusiveTotal, "1173.19")
	wantEqual(t, "amount due", res.AmountDue, "1173.19")
}

// An exact half cent rounds away from zero [BMF-2026 Rn. 17].
func TestGermanTaxRoundsExactHalfCentUp(t *testing.T) {
	for _, tc := range []struct{ taxable, rate, tax string }{
		{"2.50", "7", "0.18"},  // 0.175
		{"0.50", "19", "0.10"}, // 0.095
		{"0.10", "7", "0.01"},  // 0.007
		{"0.02", "7", "0.00"},  // 0.0014
	} {
		res := mustCalc(t, []Line{line("1", tc.taxable, tc.rate)}, de)
		wantEqual(t, tc.taxable+"@"+tc.rate, res.TaxTotal, tc.tax)
	}
}

// Tax per group, never per line: five lines of 0.03 at 19% give 0.15 × 19% =
// 0.0285 → 0.03. Rounding each line (0.0057 → 0.01) would give 0.05.
func TestTaxIsRoundedOncePerRateNotPerLine(t *testing.T) {
	lines := []Line{}
	for range 5 {
		lines = append(lines, line("1", "0.03", "19"))
	}
	res := mustCalc(t, lines, de)
	wantEqual(t, "tax", res.TaxTotal, "0.03")

	// Japan: three lines of ¥333 at 10% rounded up give ¥999 → 99.9 → ¥100;
	// per line (33.3 → 34) would be ¥102 [NTA-QA-57].
	res = mustCalc(t, []Line{line("1", "333", "10"), line("1", "333", "10"), line("1", "333", "10")}, jp(Up))
	wantEqual(t, "tax", res.TaxTotal, "100")
}

func TestJapaneseRoundingModes(t *testing.T) {
	for _, tc := range []struct {
		taxable, rate string
		want          map[Rounding]string
	}{
		{"1005", "10", map[Rounding]string{Down: "100", Up: "101", HalfUp: "101"}}, // 100.5
		{"999", "10", map[Rounding]string{Down: "99", Up: "100", HalfUp: "100"}},   // 99.9
		{"1234", "8", map[Rounding]string{Down: "98", Up: "99", HalfUp: "99"}},     // 98.72
		{"1006", "8", map[Rounding]string{Down: "80", Up: "81", HalfUp: "80"}},     // 80.48
		{"1000", "10", map[Rounding]string{Down: "100", Up: "100", HalfUp: "100"}}, // exact
	} {
		for mode, want := range tc.want {
			res := mustCalc(t, []Line{line("1", tc.taxable, tc.rate)}, jp(mode))
			wantEqual(t, tc.taxable+"@"+tc.rate+" "+string(mode), res.TaxTotal, want)
		}
	}
}

func TestJapaneseInvoiceWithBothRates(t *testing.T) {
	res := mustCalc(t, []Line{
		line("3", "333", "10"),
		line("1", "1234", "8"), // reduced rate (軽減税率)
	}, jp(Down))
	if len(res.Breakdowns) != 2 {
		t.Fatalf("breakdowns = %+v", res.Breakdowns)
	}
	wantEqual(t, "10% taxable", res.Breakdowns[0].TaxableAmount, "999")
	wantEqual(t, "10% tax", res.Breakdowns[0].TaxAmount, "99")
	wantEqual(t, "8% taxable", res.Breakdowns[1].TaxableAmount, "1234")
	wantEqual(t, "8% tax", res.Breakdowns[1].TaxAmount, "98")
	wantEqual(t, "tax inclusive", res.TaxInclusiveTotal, "2430")
}

func TestLineNetAmountRounding(t *testing.T) {
	for _, tc := range []struct {
		qty, price, base string
		minor            int32
		want             string
	}{
		{"1", "0.125", "1", 2, "0.13"},    // exact half, away from zero
		{"3", "0.333333", "1", 2, "1.00"}, // 0.999999
		{"1", "10", "3", 2, "3.33"},       // 3.333…, exact quotient
		{"2", "10", "3", 2, "6.67"},       // 6.666…
		{"1", "0.005", "1", 2, "0.01"},    // half cent
		{"1", "0.004999", "1", 2, "0.00"}, // just below half
		{"2.5", "101", "1", 0, "253"},     // 252.5 yen
		{"1", "99.4", "1", 0, "99"},       // yen price with decimals
		{"0.5", "1", "1", 0, "1"},         // 0.5 yen
		{"100", "1.5", "1000", 2, "0.15"}, // price per 1000 units
		{"0.000001", "0.000001", "1", 2, "0.00"},
	} {
		l := Line{Quantity: d(tc.qty), UnitPrice: d(tc.price), PriceBaseQuantity: d(tc.base), TaxCategory: "S", TaxRate: d("19")}
		res := mustCalc(t, []Line{l}, Params{MinorUnits: tc.minor, Rounding: HalfAwayFromZero})
		wantEqual(t, tc.qty+"×"+tc.price+"/"+tc.base, res.LineNetAmounts[0], tc.want)
	}
}

func TestRatesGroupByValue(t *testing.T) {
	res := mustCalc(t, []Line{line("1", "10", "19"), line("1", "10", "19.00"), line("1", "10", "7")}, de)
	if len(res.Breakdowns) != 2 {
		t.Fatalf("breakdowns = %+v, want 19 and 7", res.Breakdowns)
	}
	wantEqual(t, "19% taxable", res.Breakdowns[0].TaxableAmount, "20")
}

func TestNoLines(t *testing.T) {
	res := mustCalc(t, nil, Params{MinorUnits: 2, Rounding: HalfAwayFromZero, Prepaid: d("5.00")})
	if len(res.Breakdowns) != 0 || len(res.LineNetAmounts) != 0 {
		t.Errorf("res = %+v", res)
	}
	wantEqual(t, "tax inclusive", res.TaxInclusiveTotal, "0")
	wantEqual(t, "amount due", res.AmountDue, "-5.00")
}

func TestPrepaidAmount(t *testing.T) {
	p := de
	p.Prepaid = d("173.19")
	res := mustCalc(t, []Line{line("10", "95.00", "19"), line("1", "39.90", "7")}, p)
	wantEqual(t, "amount due", res.AmountDue, "1000.00")
}

func TestTooLarge(t *testing.T) {
	_, err := Calculate([]Line{line("1", "1", "19"), line("999999999999", "999999999999.999999", "19")}, de)
	var tl *TooLargeError
	if !errors.As(err, &tl) || tl.Line != 1 {
		t.Errorf("err = %v, want TooLargeError for line 1", err)
	}

	// Each line fits; their sum does not.
	_, err = Calculate([]Line{line("6000", "1000000000000", "0"), line("6000", "1000000000000", "0")}, de)
	if !errors.As(err, &tl) || tl.Line != -1 {
		t.Errorf("err = %v, want TooLargeError for a total", err)
	}

	// The largest amount that fits.
	res := mustCalc(t, []Line{line("1", "9999999999999999.99", "0")}, de)
	wantEqual(t, "total", res.TaxInclusiveTotal, "9999999999999999.99")
}

func TestInvalidInput(t *testing.T) {
	for name, tc := range map[string]struct {
		lines []Line
		p     Params
	}{
		"zero quantity":    {[]Line{line("0", "1", "19")}, de},
		"negative price":   {[]Line{line("1", "-1", "19")}, de},
		"negative rate":    {[]Line{line("1", "1", "-7")}, de},
		"zero base":        {[]Line{{Quantity: d("1"), UnitPrice: d("1"), PriceBaseQuantity: d("0"), TaxRate: d("7")}}, de},
		"unknown rounding": {[]Line{line("1", "1", "19")}, Params{MinorUnits: 2, Rounding: "banker"}},
		"negative prepaid": {nil, Params{MinorUnits: 2, Rounding: HalfAwayFromZero, Prepaid: d("-1")}},
	} {
		if _, err := Calculate(tc.lines, tc.p); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestFitsCurrency(t *testing.T) {
	for _, tc := range []struct {
		amount string
		minor  int32
		want   bool
	}{
		{"1.10", 2, true},
		{"1.105", 2, false},
		{"100", 0, true},
		{"100.0", 0, true}, // the same value; app.fits_currency agrees
		{"100.5", 0, false},
		{"9999999999999999.99", 2, true},
		{"10000000000000000", 0, false},
		{"-5.00", 2, true},
	} {
		if got := FitsCurrency(d(tc.amount), tc.minor); got != tc.want {
			t.Errorf("FitsCurrency(%s, %d) = %v, want %v", tc.amount, tc.minor, got, tc.want)
		}
	}
	if m, ok := MinorUnits("JPY"); !ok || m != 0 {
		t.Errorf("JPY = %d %v", m, ok)
	}
	if m, ok := MinorUnits("EUR"); !ok || m != 2 {
		t.Errorf("EUR = %d %v", m, ok)
	}
	if _, ok := MinorUnits("USD"); ok {
		t.Error("USD is not supported")
	}
}

// TestAgainstOracle checks random invoices against an independent
// implementation in math/big, and the BR-CO formulas on every result.
func TestAgainstOracle(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	rates := []string{"19", "7", "10", "8", "0"}
	modes := []Rounding{HalfAwayFromZero, Down, Up, HalfUp}
	for i := range 2000 {
		minor := int32(rng.IntN(2) * 2)
		p := Params{MinorUnits: minor, Rounding: modes[rng.IntN(len(modes))]}
		var lines []Line
		for range rng.IntN(8) {
			lines = append(lines, Line{
				Quantity:          decimal.New(rng.Int64N(100000)+1, -int32(rng.IntN(4))),
				UnitPrice:         decimal.New(rng.Int64N(10000000), -int32(rng.IntN(7))),
				PriceBaseQuantity: decimal.New(rng.Int64N(1000)+1, -int32(rng.IntN(3))),
				TaxCategory:       "S",
				TaxRate:           d(rates[rng.IntN(len(rates))]),
			})
		}
		res := mustCalc(t, lines, p)

		sumLines := decimal.Zero
		taxable := map[string]*big.Rat{}
		for j, l := range lines {
			exact := new(big.Rat).Quo(new(big.Rat).Mul(l.Quantity.Rat(), l.UnitPrice.Rat()), l.PriceBaseQuantity.Rat())
			want := ratRound(exact, minor, HalfAwayFromZero)
			if res.LineNetAmounts[j].Rat().Cmp(want) != 0 {
				t.Fatalf("case %d line %d: net %s, oracle %s", i, j, res.LineNetAmounts[j], want.FloatString(int(minor)))
			}
			sumLines = sumLines.Add(res.LineNetAmounts[j])
			k := l.TaxRate.String()
			if taxable[k] == nil {
				taxable[k] = new(big.Rat)
			}
			taxable[k].Add(taxable[k], want)
		}
		if len(res.Breakdowns) != len(taxable) {
			t.Fatalf("case %d: %d breakdowns, oracle %d", i, len(res.Breakdowns), len(taxable))
		}
		sumTax := decimal.Zero
		for _, b := range res.Breakdowns {
			base := taxable[b.TaxRate.String()]
			wantTax := ratRound(new(big.Rat).Quo(new(big.Rat).Mul(base, b.TaxRate.Rat()), big.NewRat(100, 1)), minor, p.Rounding)
			if b.TaxableAmount.Rat().Cmp(base) != 0 || b.TaxAmount.Rat().Cmp(wantTax) != 0 {
				t.Fatalf("case %d rate %s (%s): taxable %s tax %s, oracle %s %s", i, b.TaxRate, p.Rounding,
					b.TaxableAmount, b.TaxAmount, base.FloatString(int(minor)), wantTax.FloatString(int(minor)))
			}
			sumTax = sumTax.Add(b.TaxAmount)
		}
		// BR-CO-10, 13, 14, 15, 16.
		if !res.LineNetTotal.Equal(sumLines) || !res.TaxExclusiveTotal.Equal(res.LineNetTotal) ||
			!res.TaxTotal.Equal(sumTax) || !res.TaxInclusiveTotal.Equal(res.TaxExclusiveTotal.Add(res.TaxTotal)) ||
			!res.AmountDue.Equal(res.TaxInclusiveTotal) {
			t.Fatalf("case %d: totals break BR-CO: %+v", i, res)
		}
		for _, a := range []decimal.Decimal{res.LineNetTotal, res.TaxTotal, res.TaxInclusiveTotal} {
			if !FitsCurrency(a, minor) {
				t.Fatalf("case %d: %s does not fit %d decimals", i, a, minor)
			}
		}
	}
}

// ratRound rounds a non-negative rational to places decimals, written
// without shopspring/decimal so the test does not check the engine against
// itself.
func ratRound(r *big.Rat, places int32, mode Rounding) *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(scale))
	q, m := new(big.Int).QuoRem(scaled.Num(), scaled.Denom(), new(big.Int))
	if m.Sign() != 0 {
		switch mode {
		case Up:
			q.Add(q, big.NewInt(1))
		case HalfAwayFromZero, HalfUp:
			if new(big.Int).Mul(m, big.NewInt(2)).Cmp(scaled.Denom()) >= 0 {
				q.Add(q, big.NewInt(1))
			}
		}
	}
	return new(big.Rat).SetFrac(q, scale)
}
