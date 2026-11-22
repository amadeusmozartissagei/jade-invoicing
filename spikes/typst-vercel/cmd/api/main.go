// Command api is a PROTOTYPE (spike for ADR 0006), never merged: it checks
// that a pinned Typst binary runs inside a Vercel Go function and measures
// what a render costs.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var started = time.Now()

// root is where the embedded assets were extracted.
func root() string {
	d, err := extracted()
	if err != nil {
		log.Printf("extract: %v", err)
	}
	return d
}

// typstPath returns a runnable binary, copying it to /tmp if the bundle lost
// its executable bit.
func typstPath(r string) (string, string, error) {
	name := "typst"
	if os.PathSeparator == '\\' {
		name = "typst.exe"
	}
	p := filepath.Join(r, "bin", name)
	st, err := os.Stat(p)
	if err != nil {
		return "", "", err
	}
	if st.Mode()&0o111 != 0 || os.PathSeparator == '\\' {
		return p, "bundle", nil
	}
	tmp := filepath.Join(os.TempDir(), "typst")
	if _, err := os.Stat(tmp); err != nil {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", "", err
		}
		if err := os.WriteFile(tmp, b, 0o755); err != nil {
			return "", "", err
		}
	}
	return tmp, "tmp-copy", nil
}

func main() {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"uptime_ms":%d}`, time.Since(started).Milliseconds())
	})
	http.HandleFunc("/ls", func(w http.ResponseWriter, _ *http.Request) {
		r := root()
		cwd, _ := os.Getwd()
		fmt.Fprintf(w, "cwd=%s root=%s exe=%s tmp=%s\n", cwd, r, os.Args[0], os.TempDir())
		_ = filepath.WalkDir(r, func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() && !strings.Contains(p, "/proc") {
				if i, err := e.Info(); err == nil {
					fmt.Fprintf(w, "%v %10d %s\n", i.Mode(), i.Size(), p)
				}
			}
			return nil
		})
	})
	http.HandleFunc("/render", render)
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	log.Printf("listening on %s (init %s)", port, time.Since(started))
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// render runs: typst compile --root R --font-path R/fonts --ignore-system-fonts
// --input data=<json> [--pdf-standard a-3b --input xml=<xml>] R/templates/invoice.typ -
func render(w http.ResponseWriter, req *http.Request) {
	r := root()
	bin, how, err := typstPath(r)
	if err != nil {
		http.Error(w, "typst: "+err.Error(), 500)
		return
	}
	data := sample(req.URL.Query().Get("doc"))
	args := []string{"compile", "--root", r, "--font-path", filepath.Join(r, "fonts"), "--ignore-system-fonts",
		"--input", "data=" + data}
	if req.URL.Query().Get("std") == "a3b" {
		args = append(args, "--pdf-standard", "a-3b", "--input", "xml="+sampleXML)
	}
	args = append(args, filepath.Join(r, "templates", "invoice.typ"), "-")
	cmd := exec.Command(bin, args...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	t0 := time.Now()
	err = cmd.Run()
	wall := time.Since(t0)
	if err != nil {
		http.Error(w, fmt.Sprintf("typst failed (%s): %v\n%s", how, err, stderr.String()), 500)
		return
	}
	ps := cmd.ProcessState
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("X-Typst-How", how)
	w.Header().Set("X-Render-Wall-Ms", fmt.Sprint(wall.Milliseconds()))
	w.Header().Set("X-Render-User-Ms", fmt.Sprint(ps.UserTime().Milliseconds()))
	w.Header().Set("X-Render-Sys-Ms", fmt.Sprint(ps.SystemTime().Milliseconds()))
	w.Header().Set("X-Instance-Uptime-Ms", fmt.Sprint(time.Since(started).Milliseconds()))
	w.Header().Set("X-Pdf-Bytes", fmt.Sprint(out.Len()))
	w.Header().Set("X-Extract-Ms", fmt.Sprint(extractTook.Milliseconds()))
	_, _ = io.Copy(w, &out)
}

const sampleXML = `<?xml version="1.0" encoding="UTF-8"?><rsm:CrossIndustryInvoice xmlns:rsm="urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100"><!-- PROTOTYPE placeholder --></rsm:CrossIndustryInvoice>`

// sample returns synthetic invoice data. No real companies or numbers.
func sample(doc string) string {
	type party struct {
		Name    string `json:"name"`
		Address string `json:"address"`
		TaxID   string `json:"tax_id"`
	}
	type line struct {
		Name, Quantity, UnitPrice, TaxRate, LineNet string
	}
	v := map[string]any{}
	if doc == "jp" {
		v = map[string]any{
			"jurisdiction": "JP", "document_type": "invoice", "number": "1", "issue_date": "2026-11-22",
			"supply_date": "2026-10-30", "currency": "JPY", "total": "2,430",
			"seller":  party{"見本ソフトウェア株式会社", "東京都千代田区丸の内1-1-1", "T0000000000001"},
			"buyer":   party{"サンプル商事株式会社", "大阪府大阪市北区梅田2-2-2", ""},
			"lines":   []map[string]string{{"name": "コンサルティング", "quantity": "3", "unit_price": "333", "tax_rate": "10", "line_net_amount": "999"}, {"name": "お茶", "quantity": "1", "unit_price": "1,234", "tax_rate": "8", "line_net_amount": "1,234"}},
			"breakdown": []map[string]string{{"tax_rate": "10", "taxable_amount": "999", "tax_amount": "99"}, {"tax_rate": "8", "taxable_amount": "1,234", "tax_amount": "98"}},
		}
	} else {
		v = map[string]any{
			"jurisdiction": "DE", "document_type": "credit_note", "number": "2", "issue_date": "2026-11-22",
			"supply_date": "2026-10-30", "currency": "EUR", "total": "1.173,19",
			"preceding": map[string]string{"number": "1", "issue_date": "2026-11-21"},
			"seller":    party{"Muster Software GmbH", "Hauptstraße 5, 80331 München", "DE000000001"},
			"buyer":     party{"Beispiel Handels GmbH", "Musterstraße 1, 10115 Berlin", ""},
			"lines":     []map[string]string{{"name": "Beratung, Oktober", "quantity": "10", "unit_price": "95,00", "tax_rate": "19", "line_net_amount": "950,00"}, {"name": "Fachbuch", "quantity": "1", "unit_price": "39,90", "tax_rate": "7", "line_net_amount": "39,90"}},
			"breakdown": []map[string]string{{"tax_rate": "19", "taxable_amount": "950,00", "tax_amount": "180,50"}, {"tax_rate": "7", "taxable_amount": "39,90", "tax_amount": "2,79"}},
		}
	}
	_ = line{}
	b, _ := json.Marshal(v)
	return string(b)
}
