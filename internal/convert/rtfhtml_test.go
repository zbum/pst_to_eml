package convert

import (
	"strings"
	"testing"
)

func TestHTMLFromEncapsulatedRTF(t *testing.T) {
	const rtf = `{\rtf1\ansi\ansicpg1252\fromhtml1{\fonttbl{\f0\fswiss Arial;}}{\colortbl;\red0\green0\blue0;}` +
		`\htmlrtf{\fonttbl{\f0 Hidden;}}\htmlrtf0` +
		`{\*\htmltag64 <html><body>}` +
		`\htmlrtf ignored\htmlrtf0Hello caf\'e9` +
		`{\*\htmltag72 </body></html>}}`

	got, ok := htmlFromEncapsulatedRTF(rtf)
	if !ok {
		t.Fatal("expected html")
	}
	want := "<html><body>Hello café</body></html>"
	if got != want {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "Arial") || strings.Contains(got, "Hidden") || strings.Contains(got, "ignored") {
		t.Fatalf("rtf chrome leaked: %q", got)
	}
}

func TestHTMLFromEncapsulatedRTFUnicode(t *testing.T) {
	const rtf = `{\rtf1\ansi\fromhtml1\uc1{\*\htmltag64 <p>}\u54620?{\*\htmltag72 </p>}}`
	got, ok := htmlFromEncapsulatedRTF(rtf)
	if !ok {
		t.Fatal("expected html")
	}
	if got != "<p>한</p>" {
		t.Fatalf("got %q", got)
	}
}

func TestHTMLFromEncapsulatedRTFKeepsQuotes(t *testing.T) {
	const rtf = `{\rtf1\ansi\fromhtml1{\fonttbl{\f0 Arial;}}` +
		`{\*\htmltag8 <p>}` +
		`don\rquote t\emdash wait` +
		`{\*\htmltag4 </p>}}`
	got, ok := htmlFromEncapsulatedRTF(rtf)
	if !ok {
		t.Fatal("expected html")
	}
	if got != "<p>don’t—wait</p>" {
		t.Fatalf("got %q", got)
	}
}

func TestHTMLFromEncapsulatedRTFRejectsPlainRTF(t *testing.T) {
	if _, ok := htmlFromEncapsulatedRTF(`{\rtf1\ansi Hello}`); ok {
		t.Fatal("plain rtf was treated as html")
	}
	if _, ok := htmlFromEncapsulatedRTF(`{\rtf1\fromhtml0 Hello}`); ok {
		t.Fatal(`\fromhtml0 was treated as html`)
	}
}

func TestPlainFromRTF(t *testing.T) {
	const rtf = `{\rtf1\ansi\ansicpg1252{\fonttbl{\f0\froman Times New Roman;}}` +
		`\pard It\rquote s a test\emdash really.\par Next` +
		`{\*\generator Word}}`
	got := plainFromRTF(rtf)
	if got != "It’s a test—really.\nNext" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "Times New Roman") || strings.Contains(got, "Word") {
		t.Fatalf("destination text leaked: %q", got)
	}
}

func TestScanRTFTruncatedDoesNotPanic(t *testing.T) {
	samples := []string{"", `\`, `{\`, `{\rtf1\'`, `{\rtf1\'z`, `{\rtf1\bin`, `{\rtf1\u`, `{\*\htmltag`, "{\rtf1\\bin4 {}\x00 hi}"}
	for _, s := range samples {
		_ = scanRTF([]byte(s), true)
		_ = scanRTF([]byte(s), false)
	}
}
