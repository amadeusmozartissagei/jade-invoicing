// PROTOTYPE (spike for ADR 0006). Throwaway: renders one document from JSON
// passed as sys.inputs.data. User text only ever arrives as data, never as
// markup, so it cannot inject Typst code.
#let d = json(bytes(sys.inputs.data))
#let jp = d.jurisdiction == "JP"

#let labels = if jp {
  (
    invoice: "適格請求書", credit_note: "適格返還請求書", corrected_invoice: "修正した適格請求書",
    number: "請求書番号", issued: "発行日", supply: "取引年月日", seller: "発行者", buyer: "宛先",
    reg: "登録番号", item: "品目", qty: "数量", price: "単価", rate: "税率", net: "金額",
    base: "対象額", tax: "消費税額", total: "合計", preceding: "対象の請求書", reduced: "※軽減税率対象",
  )
} else {
  (
    invoice: "Rechnung", credit_note: "Rechnungskorrektur", corrected_invoice: "Rechnungsberichtigung",
    number: "Rechnungsnummer", issued: "Rechnungsdatum", supply: "Leistungsdatum", seller: "Rechnungssteller",
    buyer: "Rechnungsempfänger", reg: "USt-IdNr.", item: "Leistung", qty: "Menge", price: "Einzelpreis",
    rate: "USt-Satz", net: "Betrag", base: "Nettobetrag", tax: "USt", total: "Gesamtbetrag",
    preceding: "Bezug auf Rechnung", reduced: "",
  )
}

#set document(title: labels.at(d.document_type) + " " + d.number)
#set page(paper: "a4", margin: 18mm)
#set text(font: if jp { ("Noto Sans JP", "Noto Sans") } else { ("Noto Sans", "Noto Sans JP") }, size: 9pt, lang: if jp { "ja" } else { "de" })

#if "xml" in sys.inputs {
  pdf.attach("invoice.xml", bytes(sys.inputs.xml), relationship: "data", mime-type: "text/xml", description: "Invoice XML")
}

#text(16pt, weight: "bold")[#labels.at(d.document_type)]
#v(4mm)
#grid(columns: (1fr, 1fr), gutter: 6mm,
  [
    *#labels.seller* \
    #d.seller.name \
    #d.seller.address \
    #labels.reg: #d.seller.tax_id
  ],
  [
    *#labels.buyer* \
    #d.buyer.name \
    #d.buyer.address \
    #v(2mm)
    #labels.number: #d.number \
    #labels.issued: #d.issue_date \
    #labels.supply: #d.supply_date
    #if d.at("preceding", default: none) != none [ \ #labels.preceding: #d.preceding.number (#d.preceding.issue_date)]
  ],
)
#v(6mm)
#table(columns: (1fr, auto, auto, auto, auto), align: (left, right, right, right, right),
  table.header([*#labels.item*], [*#labels.qty*], [*#labels.price*], [*#labels.rate*], [*#labels.net*]),
  ..d.lines.map(l => (l.name + if jp and l.tax_rate == "8" [ ※] else [], l.quantity, l.unit_price, l.tax_rate + "%", l.line_net_amount)).flatten(),
)
#v(4mm)
#align(right, table(columns: (auto, auto, auto), align: right,
  table.header([*#labels.rate*], [*#labels.base*], [*#labels.tax*]),
  ..d.breakdown.map(b => (b.tax_rate + "%", b.taxable_amount, b.tax_amount)).flatten(),
  [*#labels.total*], [], [*#d.total #d.currency*],
))
#if jp [#labels.reduced]
