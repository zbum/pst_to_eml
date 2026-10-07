package convert

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// htmlFromEncapsulatedRTF pulls the original HTML out of an RTF body that
// Outlook wrapped with \fromhtml1 (MS-OXRTFEX). text/rtf itself is a poor
// thing to hand a mail client.
func htmlFromEncapsulatedRTF(rtf string) (string, bool) {
	raw := []byte(rtf)
	if !bytes.Contains(bytes.ToLower(raw), []byte(`\fromhtml1`)) {
		return "", false
	}
	html := strings.TrimSpace(stripNUL(scanRTF(raw, true)))
	if html == "" {
		return "", false
	}
	return html, true
}

func plainFromRTF(rtf string) string {
	return strings.TrimSpace(stripNUL(scanRTF([]byte(rtf), false)))
}

type rtfGroup struct {
	skip          bool
	capture       bool
	htmlRTF       bool
	fresh         bool
	ignorableNext bool
	uc            int
}

type rtfWriter struct {
	raw bytes.Buffer
	cp  int
	b   strings.Builder
}

func (w *rtfWriter) setCodePage(cp int) {
	if cp <= 0 || cp == w.cp {
		return
	}
	w.flush()
	w.cp = cp
}

func (w *rtfWriter) writeByte(c byte) {
	w.raw.WriteByte(c)
}

func (w *rtfWriter) writeRune(r rune) {
	w.flush()
	w.b.WriteRune(r)
}

func (w *rtfWriter) flush() {
	if w.raw.Len() == 0 {
		return
	}
	w.b.WriteString(decodeCodePage(w.raw.Bytes(), w.cp))
	w.raw.Reset()
}

func (w *rtfWriter) string() string {
	w.flush()
	return w.b.String()
}

func scanRTF(b []byte, htmlMode bool) string {
	w := rtfWriter{cp: 1252}
	stack := []rtfGroup{{fresh: true, uc: 1}}
	cur := func() *rtfGroup { return &stack[len(stack)-1] }

	for i := 0; i < len(b); {
		switch b[i] {
		case '{':
			parent := *cur()
			stack = append(stack, rtfGroup{
				skip:    parent.skip,
				capture: parent.capture,
				htmlRTF: parent.htmlRTF,
				fresh:   true,
				uc:      parent.uc,
			})
			i++
		case '}':
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			i++
		case '\\':
			if i+1 >= len(b) {
				return w.string()
			}
			i = applyRTFControl(b, i+1, cur(), &w, htmlMode)
		case '\r', '\n':
			i++
		default:
			g := cur()
			if g.ignorableNext {
				g.ignorableNext = false
				g.skip = true
			}
			g.fresh = false
			if shouldEmit(*g, htmlMode) {
				w.writeByte(b[i])
			}
			i++
		}
	}
	return w.string()
}

func shouldEmit(g rtfGroup, htmlMode bool) bool {
	if g.skip {
		return false
	}
	if !htmlMode {
		return true
	}
	if g.capture {
		return true
	}
	return !g.htmlRTF
}

func applyRTFControl(b []byte, i int, g *rtfGroup, w *rtfWriter, htmlMode bool) int {
	if i >= len(b) {
		return i
	}
	switch b[i] {
	case '\\', '{', '}':
		g.fresh = false
		if shouldEmit(*g, htmlMode) {
			w.writeByte(b[i])
		}
		return i + 1
	case '\'':
		g.fresh = false
		if i+2 < len(b) {
			hi, ok1 := fromHex(b[i+1])
			lo, ok2 := fromHex(b[i+2])
			if ok1 && ok2 {
				if shouldEmit(*g, htmlMode) {
					w.writeByte(hi<<4 | lo)
				}
				return i + 3
			}
		}
		return min(i+1, len(b))
	case '*':
		if g.fresh {
			g.ignorableNext = true
		}
		return i + 1
	case '~':
		g.fresh = false
		if shouldEmit(*g, htmlMode) {
			w.writeRune('\u00a0')
		}
		return i + 1
	case '_':
		g.fresh = false
		if shouldEmit(*g, htmlMode) {
			w.writeByte('-')
		}
		return i + 1
	case '-':
		g.fresh = false
		return i + 1
	case '\r', '\n':
		return i + 1
	default:
		if !isRTFLetter(b[i]) {
			g.fresh = false
			return i + 1
		}
	}

	word, arg, hasArg, next := parseRTFControl(b, i)
	if g.ignorableNext {
		g.ignorableNext = false
		g.fresh = false
		if htmlMode && word == "htmltag" && !g.skip {
			g.capture = true
			return next
		}
		g.skip = true
		return next
	}
	if g.fresh {
		g.fresh = false
		if isSkipDestination(word) {
			g.skip = true
			return next
		}
	}
	if r, ok := rtfPunct[word]; ok {
		if shouldEmit(*g, htmlMode) {
			w.writeRune(r)
		}
		return next
	}
	switch word {
	case "htmltag":
		if htmlMode && !g.skip {
			g.capture = true
		}
	case "htmlrtf":
		g.htmlRTF = !hasArg || arg != 0
	case "par", "line":
		if shouldEmit(*g, htmlMode) && !g.capture {
			w.writeByte('\n')
		}
	case "tab":
		if shouldEmit(*g, htmlMode) {
			w.writeByte('\t')
		}
	case "uc":
		if hasArg && arg >= 0 {
			g.uc = arg
		}
	case "ansicpg":
		if hasArg && arg > 0 {
			w.setCodePage(arg)
		}
	case "u":
		if !hasArg {
			return next
		}
		u := arg
		if u < 0 {
			u += 65536
		}
		if shouldEmit(*g, htmlMode) && u >= 0 && u <= utf8.MaxRune {
			w.writeRune(rune(u))
		}
		return skipRTFFallback(b, next, g.uc)
	case "bin":
		if hasArg && arg > 0 {
			end := next + arg
			if end > len(b) || end < next {
				end = len(b)
			}
			return end
		}
	}
	return next
}

func parseRTFControl(b []byte, i int) (word string, arg int, hasArg bool, next int) {
	start := i
	for i < len(b) && isRTFLetter(b[i]) {
		i++
	}
	word = strings.ToLower(string(b[start:i]))
	if i < len(b) && (b[i] == '-' || isRTFDigit(b[i])) {
		hasArg = true
		sign := 1
		if b[i] == '-' {
			sign = -1
			i++
		}
		n := 0
		for i < len(b) && isRTFDigit(b[i]) {
			n = n*10 + int(b[i]-'0')
			i++
		}
		arg = sign * n
	}
	if i < len(b) && b[i] == ' ' {
		i++
	}
	return word, arg, hasArg, i
}

func skipRTFFallback(b []byte, i, n int) int {
	for n > 0 && i < len(b) {
		switch b[i] {
		case '\r', '\n':
			i++
		case '\\':
			if i+1 < len(b) && b[i+1] == '\'' && i+3 < len(b) && isHex(b[i+2]) && isHex(b[i+3]) {
				i += 4
				n--
				continue
			}
			if i+1 < len(b) && isRTFLetter(b[i+1]) {
				_, _, _, i = parseRTFControl(b, i+1)
				n--
				continue
			}
			if i+1 < len(b) {
				i += 2
			} else {
				i++
			}
			n--
		default:
			i++
			n--
		}
	}
	return i
}

// Outlook writes typographic punctuation as control words. Dropping them
// turns "don't" into "dont".
var rtfPunct = map[string]rune{
	"lquote":    '\u2018',
	"rquote":    '\u2019',
	"ldblquote": '\u201c',
	"rdblquote": '\u201d',
	"endash":    '\u2013',
	"emdash":    '\u2014',
	"bullet":    '\u2022',
}

func isSkipDestination(word string) bool {
	switch word {
	case "fonttbl", "colortbl", "stylesheet", "info", "pict", "object",
		"footer", "footerf", "header", "headerf", "footnote", "field",
		"xe", "tc", "listtable", "listoverridetable", "revtbl", "xmlnstbl",
		"latentstyles", "datastore", "themedata", "colorschememapping",
		"pntext", "pnseclvl", "rsidtbl", "filetbl", "nonshppict", "shppict",
		"generator", "listtext":
		return true
	default:
		return false
	}
}

func isRTFLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isRTFDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isHex(c byte) bool {
	_, ok := fromHex(c)
	return ok
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}
