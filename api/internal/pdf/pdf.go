// Package pdf writes simple single-font text documents (invoices, reports)
// without external dependencies.
package pdf

import (
	"bytes"
	"fmt"
	"strings"
)

// Line is one line of text at a position (points from the bottom-left).
type Line struct {
	X, Y float64
	Size float64
	Bold bool
	Text string
}

// Document is a list of pages, each a list of lines (A4 portrait).
type Document struct {
	Title string
	Pages [][]Line
}

// escape makes text safe for a PDF string, replacing non-Latin-1 characters.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\' || r == '(' || r == ')':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '€':
			b.WriteString("EUR")
		case r < 32:
			b.WriteByte(' ')
		case r > 255:
			b.WriteByte('?')
		default:
			b.WriteByte(byte(r))
		}
	}
	return b.String()
}

// Bytes renders the document.
func (d Document) Bytes() []byte {
	var objs []string
	add := func(s string) int { objs = append(objs, s); return len(objs) }

	catalog := add("") // placeholder, filled below
	pagesObj := add("")
	font := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	bold := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	var kids []string
	for _, page := range d.Pages {
		var content bytes.Buffer
		for _, l := range page {
			f := "F1"
			if l.Bold {
				f = "F2"
			}
			size := l.Size
			if size == 0 {
				size = 10
			}
			fmt.Fprintf(&content, "BT /%s %.1f Tf %.2f %.2f Td (%s) Tj ET\n", f, size, l.X, l.Y, escape(l.Text))
		}
		stream := add(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()))
		pg := add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> /Contents %d 0 R >>",
			pagesObj, font, bold, stream))
		kids = append(kids, fmt.Sprintf("%d 0 R", pg))
	}
	objs[catalog-1] = fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesObj)
	objs[pagesObj-1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))
	info := add(fmt.Sprintf("<< /Title (%s) /Producer (PurrOS) >>", escape(d.Title)))

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, catalog, info, xref)
	return out.Bytes()
}
