// Invoice layout (ADR 0017). Every value comes from data.json, already
// worded and formatted by internal/invoicepdf; this file only places it.
// Values are strings shown as text, never evaluated as markup.
#let d = json(sys.inputs.data)
#let jp = d.lang == "ja"
#let muted = luma(90)

#set document(title: d.title + " " + d.meta.at(0).value)
#set page(paper: "a4", margin: (x: 18mm, top: 16mm, bottom: 18mm))
#set text(
  font: if jp { ("Noto Sans JP", "Noto Sans") } else { ("Noto Sans", "Noto Sans JP") },
  size: 9pt,
  lang: d.lang,
)
#set par(leading: 0.55em)

#let lines(xs) = xs.map(x => text(x)).join(linebreak())

#text(17pt, weight: "bold", d.title)
#v(5mm)

#grid(
  columns: (1fr, 1fr),
  gutter: 8mm,
  [
    #if d.buyer.heading != "" { text(muted, d.buyer.heading); linebreak() }
    #text(weight: "bold", d.buyer.lines.at(0, default: ""))
    #if d.buyer.lines.len() > 1 { linebreak(); lines(d.buyer.lines.slice(1)) }
  ],
  [
    #text(muted, d.seller.heading) \
    #text(weight: "bold", d.seller.lines.at(0, default: ""))
    #if d.seller.lines.len() > 1 { linebreak(); lines(d.seller.lines.slice(1)) }
  ],
)

#v(5mm)
#table(
  columns: (auto, 1fr),
  stroke: none,
  inset: (x: 0pt, y: 2pt),
  column-gutter: 6mm,
  ..d.meta.map(m => (text(muted, m.label), text(m.value))).flatten(),
)

#v(4mm)
#table(
  columns: (auto, 1fr, auto, auto, auto, auto),
  align: (left, left, right, right, right, right),
  stroke: (x, y) => if y == 0 { (bottom: 0.6pt) } else { (bottom: 0.3pt + luma(200)) },
  inset: (x: 4pt, y: 4pt),
  table.header(..d.columns.map(c => text(weight: "bold", c))),
  ..d.lines.map(l => (
    text(l.pos),
    [#text(l.name)#if l.description != "" { linebreak(); text(muted, size: 8pt, l.description) }],
    text(l.quantity),
    text(l.unit_price),
    text(l.tax_rate),
    text(l.net),
  )).flatten(),
)

#v(4mm)
#align(right, block(width: 60%)[
  #table(
    columns: (1fr, auto, auto),
    align: (left, right, right),
    stroke: (x, y) => if y == 0 { (bottom: 0.6pt) } else { none },
    inset: (x: 4pt, y: 3pt),
    table.header(..d.tax_header.map(c => text(weight: "bold", c))),
    ..d.taxes.map(r => r.map(c => text(c))).flatten(),
  )
  #v(2mm)
  #table(
    columns: (1fr, auto),
    align: (left, right),
    stroke: none,
    inset: (x: 4pt, y: 3pt),
    ..d.totals.enumerate().map(((i, t)) => {
      let w = if i == 2 { "bold" } else { "regular" }
      (text(weight: w, t.label), text(weight: w, t.value))
    }).flatten(),
  )
])

#if d.notes.len() > 0 {
  v(6mm)
  for n in d.notes { text(n); linebreak() }
}
