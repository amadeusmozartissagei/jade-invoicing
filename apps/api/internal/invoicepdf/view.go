package invoicepdf

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// view is the JSON the template reads. Every string is final: the template
// only lays it out, so all wording and formatting stay in Go.
type view struct {
	Lang      string     `json:"lang"`
	Title     string     `json:"title"`
	Seller    partyView  `json:"seller"`
	Buyer     partyView  `json:"buyer"`
	Meta      []pair     `json:"meta"`
	Columns   []string   `json:"columns"`
	Lines     []lineView `json:"lines"`
	TaxHeader []string   `json:"tax_header"`
	Taxes     [][]string `json:"taxes"`
	Totals    []pair     `json:"totals"`
	Notes     []string   `json:"notes"`
}

type partyView struct {
	Heading string   `json:"heading"`
	Lines   []string `json:"lines"`
}

type pair struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type lineView struct {
	Pos         string `json:"pos"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Quantity    string `json:"quantity"`
	UnitPrice   string `json:"unit_price"`
	TaxRate     string `json:"tax_rate"`
	Net         string `json:"net"`
}

// locale holds the words and formats of one jurisdiction.
type locale struct {
	lang              string
	titles            map[string]string
	seller, buyer     string
	number, issueDate string
	supplyDate        string
	supplyPeriod      string
	// On a credit note, Japan names the supply date the date of the return
	// and adds the original transaction's date (NTA, return invoice).
	returnDate, originalDate string
	dueDate                  string
	buyerRef, orderRef       string
	preceding                string
	vatID, taxNumber         string
	jpRegistration           string
	columns                  []string
	taxHeader                []string
	netTotal, taxTotal       string
	grossTotal               string
	prepaid, amountDue       string
	paymentTerms             string
	bank                     string
	thousands, decimals      string
	date                     func(time.Time) string
	rate                     func(decimal.Decimal) string
	money                    func(l locale, d decimal.Decimal, minor int32, currency string) string
	units                    map[string]string
}

// Titles follow from the document type alone (ADR 0017, P4). "Gutschrift"
// is never used: in German VAT law it means self-billing.
var locales = map[string]locale{
	"DE": {
		lang: "de",
		titles: map[string]string{ //nolint:gosec // G101: document titles, not credentials
			"invoice": "Rechnung", "credit_note": "Rechnungskorrektur", "corrected_invoice": "Rechnungsberichtigung",
		},
		seller: "Rechnungssteller", buyer: "Rechnungsempfänger",
		number: "Rechnungsnummer", issueDate: "Rechnungsdatum",
		supplyDate: "Leistungsdatum", supplyPeriod: "Leistungszeitraum", dueDate: "Fällig am",
		buyerRef: "Ihre Referenz", orderRef: "Bestellnummer",
		preceding: "Bezieht sich auf Rechnung",
		vatID:     "USt-IdNr.", taxNumber: "Steuernummer",
		columns:   []string{"Pos.", "Bezeichnung", "Menge", "Einzelpreis", "USt", "Betrag"},
		taxHeader: []string{"USt-Satz", "Nettobetrag", "USt-Betrag"},
		netTotal:  "Summe netto", taxTotal: "Umsatzsteuer", grossTotal: "Gesamtbetrag",
		prepaid: "Bereits gezahlt", amountDue: "Zahlbetrag",
		paymentTerms: "Zahlungsbedingungen", bank: "Bankverbindung",
		thousands: ".", decimals: ",",
		date: func(t time.Time) string { return t.Format("02.01.2006") },
		rate: func(r decimal.Decimal) string { return strings.ReplaceAll(r.String(), ".", ",") + " %" },
		money: func(l locale, d decimal.Decimal, minor int32, currency string) string {
			return l.amount(d, minor) + " " + currency
		},
		units: map[string]string{
			"C62": "Stk.", "H87": "Stk.", "HUR": "Std.", "DAY": "Tag(e)", "MON": "Monat(e)",
			"KGM": "kg", "MTR": "m", "LTR": "l", "MIN": "Min.",
		},
	},
	"JP": {
		lang: "ja",
		titles: map[string]string{ //nolint:gosec // G101: document titles, not credentials
			"invoice": "適格請求書", "credit_note": "適格返還請求書", "corrected_invoice": "修正した適格請求書",
		},
		seller: "発行者", buyer: "御中",
		number: "請求書番号", issueDate: "発行日",
		supplyDate: "取引年月日", supplyPeriod: "取引期間", dueDate: "お支払期限",
		returnDate: "返還等の年月日", originalDate: "元の取引年月日",
		buyerRef: "貴社参照番号", orderRef: "注文番号",
		preceding:      "対象の請求書",
		jpRegistration: "登録番号", vatID: "VAT ID", taxNumber: "税番号",
		columns:   []string{"No.", "品目", "数量", "単価", "税率", "金額"},
		taxHeader: []string{"税率区分", "対象額", "消費税額"},
		netTotal:  "小計（税抜）", taxTotal: "消費税", grossTotal: "合計（税込）",
		prepaid: "前受金", amountDue: "ご請求金額",
		paymentTerms: "お支払条件", bank: "お振込先",
		thousands: ",", decimals: ".",
		date: func(t time.Time) string { return fmt.Sprintf("%d年%d月%d日", t.Year(), t.Month(), t.Day()) },
		rate: func(r decimal.Decimal) string { return r.String() + "%" },
		money: func(l locale, d decimal.Decimal, minor int32, currency string) string {
			if currency == "JPY" {
				return l.amount(d, minor) + "円"
			}
			return l.amount(d, minor) + " " + currency
		},
		units: map[string]string{
			"C62": "個", "H87": "個", "HUR": "時間", "DAY": "日", "MON": "か月",
			"KGM": "kg", "MTR": "m", "LTR": "L", "MIN": "分",
		},
	},
}

// reducedRate is Japan's reduced consumption tax rate. Lines taxed at it are
// marked ※ on a qualified invoice (NTA required item 3).
var reducedRate = decimal.NewFromInt(8)

const reducedMark = "※"

func buildView(in Input) (view, error) {
	l, ok := locales[in.Jurisdiction]
	if !ok {
		return view{}, fmt.Errorf("invoicepdf: unknown jurisdiction %q", in.Jurisdiction)
	}
	title, ok := l.titles[in.DocumentType]
	if !ok {
		return view{}, fmt.Errorf("invoicepdf: unknown document type %q", in.DocumentType)
	}
	jp := in.Jurisdiction == "JP"
	money := func(d decimal.Decimal) string { return l.money(l, d, in.MinorUnits, in.Currency) }

	// Empty lists, not nil: the template iterates them, and nil would be
	// JSON null.
	v := view{
		Lang: l.lang, Title: title, Columns: l.columns, TaxHeader: l.taxHeader,
		Meta: []pair{}, Lines: []lineView{}, Taxes: [][]string{}, Totals: []pair{}, Notes: []string{},
	}
	v.Seller = partyView{Heading: l.seller, Lines: l.partyLines(in.Seller, in.Jurisdiction, true)}
	v.Buyer = partyView{Lines: l.partyLines(in.Buyer, in.Jurisdiction, false)}
	if jp {
		// A Japanese document addresses the buyer as "<name> 御中".
		if len(v.Buyer.Lines) > 0 {
			v.Buyer.Lines[0] += " " + l.buyer
		}
	} else {
		v.Buyer.Heading = l.buyer
	}

	add := func(label, value string) { v.Meta = append(v.Meta, pair{label, value}) }
	add(l.number, in.Number)
	add(l.issueDate, l.date(in.IssueDate))
	returnInvoice := jp && in.DocumentType == "credit_note"
	dateLabel, periodLabel := l.supplyDate, l.supplyPeriod
	if returnInvoice {
		dateLabel, periodLabel = l.returnDate, l.returnDate
	}
	if s := l.dateOrPeriod(in.SupplyDate, in.SupplyPeriodStart, in.SupplyPeriodEnd); s != "" {
		label := dateLabel
		if in.SupplyDate == nil {
			label = periodLabel
		}
		add(label, s)
	}
	if returnInvoice && in.Preceding != nil {
		p := in.Preceding
		if s := l.dateOrPeriod(p.SupplyDate, p.SupplyPeriodStart, p.SupplyPeriodEnd); s != "" {
			add(l.originalDate, s)
		}
	}
	if in.Preceding != nil {
		ref := in.Preceding.Number + " vom " + l.date(in.Preceding.IssueDate)
		if jp {
			ref = in.Preceding.Number + "（" + l.date(in.Preceding.IssueDate) + "発行）"
		}
		add(l.preceding, ref)
	}
	if in.DueDate != nil {
		add(l.dueDate, l.date(*in.DueDate))
	}
	if s := deref(in.BuyerReference); s != "" && s != "-" {
		add(l.buyerRef, s)
	}
	if s := deref(in.OrderReference); s != "" {
		add(l.orderRef, s)
	}

	marked := false
	for i, ln := range in.Lines {
		name := ln.Name
		if jp && ln.TaxRate.Equal(reducedRate) {
			name += " " + reducedMark
			marked = true
		}
		desc := ln.Description
		if ln.PeriodStart != nil && ln.PeriodEnd != nil {
			p := l.date(*ln.PeriodStart) + " – " + l.date(*ln.PeriodEnd)
			if desc != "" {
				desc += "\n"
			}
			desc += p
		}
		v.Lines = append(v.Lines, lineView{
			Pos:         strconv.Itoa(i + 1),
			Name:        name,
			Description: desc,
			Quantity:    l.quantity(ln.Quantity) + " " + l.unit(ln.UnitCode),
			UnitPrice:   l.unitPrice(ln, in.MinorUnits),
			TaxRate:     l.rate(ln.TaxRate),
			Net:         l.amount(ln.NetAmount, in.MinorUnits),
		})
	}

	for _, b := range in.Breakdown {
		label := l.rate(b.TaxRate)
		if jp {
			label += "対象"
		}
		v.Taxes = append(v.Taxes, []string{label, money(b.TaxableAmount), money(b.TaxAmount)})
	}

	total := func(label string, d decimal.Decimal) { v.Totals = append(v.Totals, pair{label, money(d)}) }
	total(l.netTotal, in.TaxExclusiveTotal)
	total(l.taxTotal, in.TaxTotal)
	total(l.grossTotal, in.TaxInclusiveTotal)
	if in.PrepaidAmount.IsPositive() {
		total(l.prepaid, in.PrepaidAmount)
		total(l.amountDue, in.AmountDue)
	}

	if marked {
		v.Notes = append(v.Notes, reducedMark+"は軽減税率対象")
	}
	if s := deref(in.PaymentTerms); s != "" {
		v.Notes = append(v.Notes, l.paymentTerms+": "+s)
	}
	if bank := l.bankLine(in.Seller); bank != "" {
		v.Notes = append(v.Notes, bank)
	}
	if s := deref(in.Note); s != "" {
		v.Notes = append(v.Notes, s)
	}
	return v, nil
}

func (l locale) partyLines(p Party, jurisdiction string, seller bool) []string {
	var out []string
	if p.Name != "" {
		out = append(out, p.Name)
	}
	if jurisdiction == "JP" {
		if p.PostCode != "" {
			out = append(out, "〒"+p.PostCode)
		}
		if addr := p.City + strings.Join(p.AddressLines, ""); addr != "" {
			out = append(out, addr)
		}
	} else {
		out = append(out, p.AddressLines...)
		if city := strings.TrimSpace(p.PostCode + " " + p.City); city != "" {
			out = append(out, city)
		}
	}
	if p.CountryCode != "" && p.CountryCode != jurisdiction {
		out = append(out, p.CountryCode)
	}
	if p.JpRegistrationNumber != "" {
		out = append(out, l.jpRegistration+": "+p.JpRegistrationNumber)
	}
	if p.VatID != "" {
		out = append(out, l.vatID+": "+p.VatID)
	}
	if seller && p.TaxNumber != "" {
		out = append(out, l.taxNumber+": "+p.TaxNumber)
	}
	return out
}

func (l locale) bankLine(p Party) string {
	var parts []string
	if p.AccountName != "" {
		parts = append(parts, p.AccountName)
	}
	if p.IBAN != "" {
		parts = append(parts, "IBAN "+p.IBAN)
	}
	if p.BIC != "" {
		parts = append(parts, "BIC "+p.BIC)
	}
	if len(parts) == 0 || p.IBAN == "" {
		return ""
	}
	return l.bank + ": " + strings.Join(parts, ", ")
}

func (l locale) dateOrPeriod(d, start, end *time.Time) string {
	switch {
	case d != nil:
		return l.date(*d)
	case start != nil && end != nil:
		return l.date(*start) + " – " + l.date(*end)
	}
	return ""
}

// unitPrice adds the base quantity when the price is not per one unit, so
// quantity × price = amount still reads correctly.
func (l locale) unitPrice(ln Line, minor int32) string {
	p := l.amount(ln.UnitPrice, minor)
	if ln.PriceBaseQuantity.IsZero() || ln.PriceBaseQuantity.Equal(decimal.NewFromInt(1)) {
		return p
	}
	return p + " / " + l.quantity(ln.PriceBaseQuantity) + " " + l.unit(ln.UnitCode)
}

func (l locale) unit(code string) string {
	if u, ok := l.units[code]; ok {
		return u
	}
	return code
}

// quantity prints a quantity without trailing zeros.
func (l locale) quantity(d decimal.Decimal) string { return l.amount(d, 0) }

// amount groups thousands and uses the locale's decimal mark. It prints at
// least minor decimals, and more when the value has more (a unit price may),
// so nothing is ever rounded here.
func (l locale) amount(d decimal.Decimal, minor int32) string {
	s := d.String()                                                  // no trailing zeros
	if _, frac, _ := strings.Cut(s, "."); int32(len(frac)) < minor { //nolint:gosec // G115: decimal places of an amount
		s = d.StringFixed(minor)
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteString(l.thousands)
		}
		b.WriteRune(c)
	}
	out := b.String()
	if frac != "" {
		out += l.decimals + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
