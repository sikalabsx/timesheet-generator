package main

import (
	"strings"
	"unicode"

	"github.com/go-pdf/fpdf"
)

// Minimal Markdown support for the header and footer:
//
//	# H1, ## H2, ### H3      headings (bold, larger)
//	- item / * item          bullets
//	**bold** / __bold__
//	*italic* / _italic_
//	`code`
//	[text](url)              clickable link
//	<https://x>, <a@b.cz>    autolinks (URL, e-mail)
//	\*                       escape

type mdSpan struct {
	text   string
	bold   bool
	italic bool
	code   bool
	link   string
}

type mdLine struct {
	scale  float64 // font size multiplier
	bold   bool
	bullet bool
	spans  []mdSpan
}

const ptToMM = 25.4 / 72

func parseMarkdown(s string) []mdLine {
	var lines []mdLine
	for _, raw := range textLines(s) {
		l := mdLine{scale: 1}
		t := strings.TrimRight(raw, " \t")
		switch {
		case strings.HasPrefix(t, "### "):
			l.scale, l.bold, t = 1.15, true, t[4:]
		case strings.HasPrefix(t, "## "):
			l.scale, l.bold, t = 1.3, true, t[3:]
		case strings.HasPrefix(t, "# "):
			l.scale, l.bold, t = 1.6, true, t[2:]
		case strings.HasPrefix(t, "- "), strings.HasPrefix(t, "* "):
			l.bullet, t = true, t[2:]
		}
		l.spans = parseInline(t)
		lines = append(lines, l)
	}
	return lines
}

func parseInline(s string) []mdSpan {
	var spans []mdSpan
	var buf strings.Builder
	bold, italic := false, false
	r := []rune(s)

	flush := func() {
		if buf.Len() > 0 {
			spans = append(spans, mdSpan{text: buf.String(), bold: bold, italic: italic})
			buf.Reset()
		}
	}
	isWord := func(i int) bool {
		return i >= 0 && i < len(r) && (unicode.IsLetter(r[i]) || unicode.IsDigit(r[i]))
	}
	indexFrom := func(from int, sub string) int {
		if from > len(r) {
			return -1
		}
		i := strings.Index(string(r[from:]), sub)
		if i < 0 {
			return -1
		}
		return from + len([]rune(string(r[from:])[:i]))
	}

	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\\' && i+1 < len(r):
			i++
			buf.WriteRune(r[i])
		case c == '`':
			if end := indexFrom(i+1, "`"); end >= 0 {
				flush()
				spans = append(spans, mdSpan{text: string(r[i+1 : end]), code: true})
				i = end
			} else {
				buf.WriteRune(c)
			}
		case c == '[':
			closeText := indexFrom(i+1, "](")
			closeURL := -1
			if closeText >= 0 {
				closeURL = indexFrom(closeText+2, ")")
			}
			if closeText >= 0 && closeURL >= 0 {
				flush()
				spans = append(spans, mdSpan{
					text:   string(r[i+1 : closeText]),
					bold:   bold,
					italic: italic,
					link:   string(r[closeText+2 : closeURL]),
				})
				i = closeURL
			} else {
				buf.WriteRune(c)
			}
		case c == '<':
			end := indexFrom(i+1, ">")
			target := ""
			if end >= 0 {
				target = string(r[i+1 : end])
			}
			if url := autolink(target); url != "" {
				flush()
				spans = append(spans, mdSpan{text: target, bold: bold, italic: italic, link: url})
				i = end
			} else {
				buf.WriteRune(c)
			}
		case c == '*' || c == '_':
			n := 1
			for i+n < len(r) && r[i+n] == c {
				n++
			}
			marker := string(r[i : i+n])
			// Literal: runs of 3+ (e.g. "____" signature lines), intraword "_"
			// (snake_case), or an opening marker without a matching closing one.
			literal := n > 2 ||
				(c == '_' && isWord(i-1) && isWord(i+n)) ||
				(n == 2 && !bold && indexFrom(i+n, marker) < 0) ||
				(n == 1 && !italic && indexFrom(i+n, marker) < 0)
			if literal {
				buf.WriteString(marker)
			} else {
				flush()
				if n == 2 {
					bold = !bold
				} else {
					italic = !italic
				}
			}
			i += n - 1
		default:
			buf.WriteRune(c)
		}
	}
	flush()
	return spans
}

// autolink returns the link target for <https://...> or <user@domain>, or "" if s is neither.
func autolink(s string) string {
	if s == "" || strings.ContainsAny(s, " \t<") {
		return ""
	}
	if i := strings.Index(s, "://"); i > 0 {
		return s
	}
	if at := strings.Index(s, "@"); at > 0 && strings.Contains(s[at+1:], ".") {
		return "mailto:" + s
	}
	return ""
}

func (l mdLine) height(baseSize, lineSpacing float64) float64 {
	return baseSize * l.scale * ptToMM * lineSpacing
}

func markdownHeight(lines []mdLine, baseSize, lineSpacing float64) float64 {
	var h float64
	for _, l := range lines {
		h += l.height(baseSize, lineSpacing)
	}
	return h
}

// renderMarkdown writes lines at the current position, each starting at the left margin.
func renderMarkdown(pdf *fpdf.Fpdf, lines []mdLine, baseSize, lineSpacing float64) {
	left, _, _, _ := pdf.GetMargins()
	for _, l := range lines {
		size := baseSize * l.scale
		h := l.height(baseSize, lineSpacing)
		pdf.SetX(left)
		if l.bullet {
			pdf.SetFont("Go", "", size)
			pdf.Write(h, "•  ")
		}
		for _, s := range l.spans {
			family, style := "Go", ""
			if s.code {
				family = "GoMono"
			} else {
				if s.bold || l.bold {
					style += "B"
				}
				if s.italic {
					style += "I"
				}
			}
			if s.link != "" {
				style += "U" // links stay black, underlined
			}
			pdf.SetFont(family, style, size)
			if s.link != "" {
				pdf.WriteLinkString(h, s.text, s.link)
			} else {
				pdf.Write(h, s.text)
			}
		}
		pdf.Ln(h)
	}
}
