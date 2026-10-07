package convert

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/unicode"
)

func TestDecodeHTMLBytes(t *testing.T) {
	t.Run("utf-8", func(t *testing.T) {
		in := "<p>안녕</p>"
		if got := decodeHTMLBytes([]byte(in), 0); got != in {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("utf-16le bom", func(t *testing.T) {
		enc := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder()
		raw, err := enc.Bytes([]byte("<p>안녕</p>"))
		if err != nil {
			t.Fatal(err)
		}
		if got := decodeHTMLBytes(raw, 0); got != "<p>안녕</p>" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("utf-16le without bom", func(t *testing.T) {
		enc := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder()
		raw, err := enc.Bytes([]byte("<p>Hi</p>"))
		if err != nil {
			t.Fatal(err)
		}
		if got := decodeHTMLBytes(raw, 0); got != "<p>Hi</p>" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("windows-1252", func(t *testing.T) {
		raw := []byte("<p>caf\xe9</p>")
		if got := decodeHTMLBytes(raw, 1252); got != "<p>café</p>" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("meta charset", func(t *testing.T) {
		raw := []byte("<html><head><meta charset=\"windows-1252\"></head><body>caf\xe9</body></html>")
		got := decodeHTMLBytes(raw, 0)
		if !strings.Contains(got, "café") || !strings.Contains(got, "<body>") {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("trailing nul", func(t *testing.T) {
		raw := []byte("<p>ok</p>\x00")
		if got := decodeHTMLBytes(raw, 65001); got != "<p>ok</p>" {
			t.Fatalf("got %q", got)
		}
	})
}
