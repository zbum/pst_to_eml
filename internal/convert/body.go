package convert

import (
	"bytes"
	"cmp"
	"regexp"
	"strings"
	"unicode/utf8"

	pst "github.com/mooijtech/go-pst/v6/pkg"
	"github.com/mooijtech/go-pst/v6/pkg/properties"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/unicode"
)

// PidTagHtml is 0x1013. Outlook stores it as a binary property, and
// GetBodyHtml only reads the Unicode-string form, so the HTML would be dropped.
const (
	pidTagHtml             uint16 = 4115
	pidTagInternetCodepage uint16 = 16350
	maxHTMLBytes                  = 32 << 20
)

func fillBodies(msg *pst.Message, props *properties.Message, mail *Mail) {
	mail.Plain = stripNUL(props.GetBody())
	mail.HTML = strings.TrimSpace(stripNUL(props.GetBodyHtml()))
	if mail.HTML == "" {
		mail.HTML = htmlFromProperty(msg)
	}
	if mail.HTML != "" {
		return
	}
	rtf, ok := compressedRTF(msg)
	if !ok {
		return
	}
	if html, ok := htmlFromEncapsulatedRTF(rtf); ok {
		mail.HTML = html
		return
	}
	if mail.Plain == "" {
		mail.Plain = plainFromRTF(rtf)
	}
}

// compressedRTF reads PidTagRtfCompressed. The decoder panics when the
// property is shorter than an RTF compression header, so a bad item must
// not abort the whole PST.
func compressedRTF(msg *pst.Message) (rtf string, ok bool) {
	defer func() {
		if recover() != nil {
			rtf, ok = "", false
		}
	}()
	s, err := msg.GetBodyRTF()
	if err != nil || s == "" {
		return "", false
	}
	return s, true
}

func htmlFromProperty(msg *pst.Message) string {
	r, ok := propertyReader(msg, pidTagHtml)
	if !ok {
		return ""
	}
	switch r.Property.Type {
	case pst.PropertyTypeString:
		s, err := r.GetString()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(stripNUL(s))
	case pst.PropertyTypeString8:
		s, err := r.GetString8(cmp.Or(internetCodePage(msg), 1252))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(stripNUL(s))
	case pst.PropertyTypeBinary:
		buf := binaryProperty(r)
		if len(buf) == 0 {
			return ""
		}
		return decodeHTMLBytes(buf, internetCodePage(msg))
	default:
		return ""
	}
}

func propertyReader(msg *pst.Message, id uint16) (pst.PropertyReader, bool) {
	if msg == nil || msg.PropertyContext == nil {
		return pst.PropertyReader{}, false
	}
	r, err := msg.PropertyContext.GetPropertyReader(id, msg.LocalDescriptors)
	if err != nil {
		return pst.PropertyReader{}, false
	}
	return r, true
}

func binaryProperty(r pst.PropertyReader) []byte {
	if r.HeapOnNodeReader != nil {
		n := r.Size()
		if n <= 0 || n > maxHTMLBytes {
			return nil
		}
		buf := make([]byte, n)
		got, err := r.ReadAt(buf, 0)
		if got <= 0 || (err != nil && got < len(buf)) {
			return nil
		}
		return buf[:got]
	}
	if len(r.Property.Data) > 0 {
		return bytes.Clone(r.Property.Data)
	}
	return nil
}

func internetCodePage(msg *pst.Message) int {
	r, ok := propertyReader(msg, pidTagInternetCodepage)
	if !ok {
		return 0
	}
	v, err := r.GetInteger32()
	if err != nil || v <= 0 {
		return 0
	}
	return int(v)
}

func decodeHTMLBytes(b []byte, codePage int) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) >= 2 {
		switch {
		case b[0] == 0xFF && b[1] == 0xFE:
			return cleanupText(decodeUTF16(b[2:], unicode.LittleEndian))
		case b[0] == 0xFE && b[1] == 0xFF:
			return cleanupText(decodeUTF16(b[2:], unicode.BigEndian))
		}
	}
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		b = b[3:]
	}
	switch {
	case looksLikeUTF16LE(b):
		return cleanupText(decodeUTF16(b, unicode.LittleEndian))
	case looksLikeUTF16BE(b):
		return cleanupText(decodeUTF16(b, unicode.BigEndian))
	}
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return cleanupText(string(b))
	}
	if name := sniffCharset(b); name != "" && !isUTF8Name(name) {
		if s, ok := decodeNamed(b, name); ok {
			return cleanupText(s)
		}
	}
	if s := decodeCodePage(b, codePage); strings.TrimSpace(s) != "" {
		return cleanupText(s)
	}
	return cleanupText(strings.ToValidUTF8(string(b), ""))
}

func cleanupText(s string) string {
	return strings.TrimSpace(stripNUL(s))
}

func looksLikeUTF16LE(b []byte) bool {
	return utf16ZeroRatio(b, false)
}

func looksLikeUTF16BE(b []byte) bool {
	return utf16ZeroRatio(b, true)
}

func utf16ZeroRatio(b []byte, highByteZero bool) bool {
	if len(b) < 8 || len(b)%2 != 0 {
		return false
	}
	n := min(len(b), 80)
	zeros, pairs := 0, 0
	for i := 0; i+1 < n; i += 2 {
		pairs++
		lo, hi := b[i], b[i+1]
		if highByteZero {
			lo, hi = hi, lo
		}
		if hi == 0 && lo != 0 {
			zeros++
		}
	}
	return pairs > 0 && zeros*2 >= pairs
}

func decodeUTF16(b []byte, order unicode.Endianness) string {
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	if len(b) == 0 {
		return ""
	}
	s, err := unicode.UTF16(order, unicode.IgnoreBOM).NewDecoder().String(string(b))
	if s == "" && err != nil {
		return ""
	}
	return s
}

var metaCharset = regexp.MustCompile(`(?i)(?:charset|encoding)\s*=\s*["']?([a-zA-Z0-9._+-]+)`)

func sniffCharset(b []byte) string {
	head := b
	if len(head) > 4096 {
		head = head[:4096]
	}
	m := metaCharset.FindSubmatch(head)
	if m == nil {
		return ""
	}
	return string(m[1])
}

func isUTF8Name(name string) bool {
	switch strings.ToLower(name) {
	case "utf-8", "utf8":
		return true
	default:
		return false
	}
}

func decodeCodePage(b []byte, cp int) string {
	if len(b) == 0 {
		return ""
	}
	switch cp {
	case 65001, 20127:
		return strings.ToValidUTF8(string(b), "")
	case 1200:
		return decodeUTF16(b, unicode.LittleEndian)
	case 1201:
		return decodeUTF16(b, unicode.BigEndian)
	}
	if cp == 0 {
		cp = 1252
	}
	if s, ok := decodeNamed(b, pst.CodePageIdentifierToEncoding[cp]); ok {
		return s
	}
	s, err := charmap.Windows1252.NewDecoder().String(string(b))
	if s == "" && err != nil {
		return strings.ToValidUTF8(string(b), "")
	}
	return stripNUL(s)
}

func decodeNamed(b []byte, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	enc, err := ianaindex.IANA.Encoding(name)
	if err != nil || enc == nil {
		enc, err = ianaindex.MIME.Encoding(name)
	}
	if (err != nil || enc == nil) && strings.EqualFold(name, "ks_c_5601-1987") {
		enc, err = ianaindex.IANA.Encoding("euc-kr")
	}
	if err != nil || enc == nil {
		return "", false
	}
	s, decErr := enc.NewDecoder().String(string(b))
	if s == "" && decErr != nil {
		return "", false
	}
	return stripNUL(s), true
}
