package convert

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestWriteEMLPlainKorean(t *testing.T) {
	var buf bytes.Buffer
	when := time.Date(2024, 3, 2, 9, 8, 7, 0, time.UTC)
	err := WriteEML(&buf, Mail{
		Subject:   "제목 테스트",
		FromName:  "홍길동",
		FromEmail: "hong@example.com",
		To:        "받는 사람",
		Date:      when,
		Plain:     "본문입니다",
		MessageID: "<m1@example.com>",
	})
	if err != nil {
		t.Fatalf("WriteEML: %v", err)
	}

	msg, err := mail.ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("subject: %v", err)
	}
	if subject != "제목 테스트" {
		t.Fatalf("subject = %q", subject)
	}
	from, err := msg.Header.AddressList("From")
	if err != nil {
		t.Fatalf("from: %v", err)
	}
	if len(from) != 1 || from[0].Address != "hong@example.com" || from[0].Name != "홍길동" {
		t.Fatalf("from = %+v", from)
	}
	if msg.Header.Get("Message-ID") != "<m1@example.com>" {
		t.Fatalf("message-id = %q", msg.Header.Get("Message-Id"))
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	if string(body) != "본문입니다" {
		t.Fatalf("body = %q", body)
	}
}

func TestWriteEMLStripsHeaderInjection(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEML(&buf, Mail{
		Subject:   "hello\r\nBcc: evil@example.com",
		FromEmail: "a@example.com",
		Plain:     "x",
	})
	if err != nil {
		t.Fatalf("WriteEML: %v", err)
	}
	raw := buf.String()
	if strings.Contains(raw, "\nBcc:") || strings.Contains(raw, "\rBcc:") {
		t.Fatalf("injected header survived:\n%s", raw)
	}
}

func TestWriteEMLStripsControls(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEML(&buf, Mail{
		FromName:  "Mailbox\x00",
		FromEmail: "a@example.com",
		To:        "bob@example.com",
		Cc:        "\x00",
		Bcc:       "\x00",
		Subject:   "제목\x00",
		Plain:     "x",
	})
	if err != nil {
		t.Fatalf("WriteEML: %v", err)
	}
	raw := buf.String()
	if strings.Contains(raw, "\x00") {
		t.Fatalf("NUL survived:\n%s", raw)
	}
	if strings.Contains(raw, "Cc:") || strings.Contains(raw, "Bcc:") {
		t.Fatalf("empty recipient header survived:\n%s", raw)
	}
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	from, err := msg.Header.AddressList("From")
	if err != nil {
		t.Fatalf("from: %v", err)
	}
	if len(from) != 1 || from[0].Name != "Mailbox" || from[0].Address != "a@example.com" {
		t.Fatalf("from = %+v", from)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("subject: %v", err)
	}
	if subject != "제목" {
		t.Fatalf("subject = %q", subject)
	}
}

func TestWriteEMLAttachment(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEML(&buf, Mail{
		FromEmail: "a@example.com",
		Subject:   "file",
		Plain:     "see file",
		HTML:      "<p>see file</p>",
		Attachments: []Attachment{{
			Name:        "노트.txt",
			ContentType: "text/plain",
			Data:        []byte("첨부"),
		}},
	})
	if err != nil {
		t.Fatalf("WriteEML: %v", err)
	}
	msg, err := mail.ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("media: %v", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("media = %s", mediaType)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var names []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("part: %v", err)
		}
		if disp := part.Header.Get("Content-Disposition"); disp != "" {
			_, dispParams, err := mime.ParseMediaType(disp)
			if err != nil {
				t.Fatalf("disposition: %v", err)
			}
			names = append(names, dispParams["filename"])
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read part: %v", err)
			}
			decoded, err := base64.StdEncoding.DecodeString(stripSpace(string(data)))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if string(decoded) != "첨부" {
				t.Fatalf("attachment = %q", decoded)
			}
		}
	}
	if len(names) != 1 || names[0] != "노트.txt" {
		t.Fatalf("filenames = %#v", names)
	}
}

func TestWriteEMLAlternativeRelatedAndMixed(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEML(&buf, Mail{
		FromEmail: "a@example.com",
		Subject:   "html",
		Plain:     "hello",
		HTML:      `<html><body><img src="cid:logo@x"></body></html>`,
		Attachments: []Attachment{
			{Name: "logo.png", ContentType: "image/png", ContentID: "<logo@x>", Data: []byte{1, 2, 3, 4}},
			{Name: "notes.txt", ContentType: "text/plain", Data: []byte("note")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := parseEML(t, buf.String())
	if root.media != "multipart/mixed" {
		t.Fatalf("root = %s", root.media)
	}
	if len(root.parts) != 2 || root.parts[0].media != "multipart/related" {
		t.Fatalf("mixed parts = %#v", mediaNames(root.parts))
	}
	if !strings.Contains(root.parts[0].contentType, `type="multipart/alternative"`) {
		t.Fatalf("related type = %s", root.parts[0].contentType)
	}
	related := root.parts[0]
	if len(related.parts) != 2 || related.parts[0].media != "multipart/alternative" {
		t.Fatalf("related parts = %#v", mediaNames(related.parts))
	}
	alt := related.parts[0]
	if len(alt.parts) != 2 || alt.parts[0].body != "hello" || !strings.Contains(alt.parts[1].body, `cid:logo@x`) {
		t.Fatalf("alternative = plain %q html %q", alt.parts[0].body, alt.parts[1].body)
	}
	if alt.parts[1].media != "text/html" {
		t.Fatalf("html media = %s", alt.parts[1].media)
	}
	img := related.parts[1]
	if img.media != "image/png" || img.body != "\x01\x02\x03\x04" {
		t.Fatalf("image = %s %q", img.media, img.body)
	}
	if !strings.Contains(strings.ToLower(img.disp), "inline") || !strings.Contains(img.cid, "logo@x") {
		t.Fatalf("image headers disp=%q cid=%q", img.disp, img.cid)
	}
	file := root.parts[1]
	if file.media != "text/plain" || file.body != "note" || !strings.Contains(strings.ToLower(file.disp), "attachment") {
		t.Fatalf("file = %s %q %s", file.media, file.body, file.disp)
	}
}

func TestWriteEMLRelatedHTMLOnly(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEML(&buf, Mail{
		FromEmail: "a@example.com",
		HTML:      `<img src="cid:logo.png">`,
		Attachments: []Attachment{{
			Name:        "logo.png",
			ContentType: "image/png",
			Data:        []byte("PNG"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := parseEML(t, buf.String())
	if root.media != "multipart/related" {
		t.Fatalf("root = %s %s", root.media, root.contentType)
	}
	if len(root.parts) != 2 || root.parts[0].media != "text/html" || root.parts[1].media != "image/png" {
		t.Fatalf("parts = %#v", mediaNames(root.parts))
	}
	if root.parts[1].body != "PNG" || !strings.Contains(root.parts[1].cid, "logo.png") {
		t.Fatalf("image cid=%q body=%q", root.parts[1].cid, root.parts[1].body)
	}
}

func TestHTMLRefersDoesNotMatchCIDPrefix(t *testing.T) {
	html := `<img src="cid:logo@x">`
	if !htmlRefers(html, Attachment{ContentID: "logo@x", Name: "other.png"}) {
		t.Fatal("full cid should match")
	}
	if htmlRefers(html, Attachment{ContentID: "logo", Name: "logo"}) {
		t.Fatal("prefix cid matched a longer reference")
	}
}

func TestWriteEMLKeepsUnreferencedCIDVisible(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEML(&buf, Mail{
		FromEmail: "a@example.com",
		HTML:      "<p>no image</p>",
		Attachments: []Attachment{{
			Name:        "a.png",
			ContentType: "image/png",
			ContentID:   "a@x",
			Inline:      true,
			Data:        []byte{9},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := parseEML(t, buf.String())
	if root.media != "multipart/mixed" {
		t.Fatalf("root = %s", root.media)
	}
	if len(root.parts) != 2 || root.parts[0].media != "text/html" || root.parts[1].media != "image/png" {
		t.Fatalf("parts = %#v", mediaNames(root.parts))
	}
	if strings.Contains(strings.ToLower(root.parts[1].disp), "inline") || root.parts[1].cid != "" {
		t.Fatalf("unreferenced image hidden: disp=%q cid=%q", root.parts[1].disp, root.parts[1].cid)
	}
}

func TestWriteZipNames(t *testing.T) {
	var buf bytes.Buffer
	err := WriteZip(&buf, []Mail{
		{Folder: "받은 편지함", Subject: "하나/둘", Plain: "a", FromEmail: "a@example.com"},
		{Folder: "받은 편지함", Subject: "둘", Plain: "b", FromEmail: "b@example.com"},
	})
	if err != nil {
		t.Fatalf("WriteZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("files = %d", len(zr.File))
	}
	if zr.File[0].Name != "받은 편지함/000001-하나_둘.eml" {
		t.Fatalf("name0 = %q", zr.File[0].Name)
	}
	if zr.File[1].Name != "받은 편지함/000002-둘.eml" {
		t.Fatalf("name1 = %q", zr.File[1].Name)
	}
}

func TestConvertRejectsNonPST(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/no.pst"
	dst := dir + "/out.zip"
	if err := osWrite(src, []byte("this is not a pst")); err != nil {
		t.Fatal(err)
	}
	_, err := Convert(src, dst, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := osStat(dst); statErr == nil {
		t.Fatal("zip left behind after a failed convert")
	}
}

type partView struct {
	media       string
	contentType string
	disp        string
	cid         string
	body        string
	parts       []partView
}

func parseEML(t *testing.T, raw string) partView {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, raw)
	}
	return readEntity(t, msg.Header, msg.Body)
}

func readEntity(t *testing.T, h interface{ Get(string) string }, r io.Reader) partView {
	t.Helper()
	ctype := h.Get("Content-Type")
	media, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		t.Fatalf("content-type %q: %v", ctype, err)
	}
	view := partView{
		media:       media,
		contentType: ctype,
		disp:        h.Get("Content-Disposition"),
		cid:         h.Get("Content-Id"),
	}
	if strings.HasPrefix(media, "multipart/") {
		mr := multipart.NewReader(r, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("part: %v", err)
			}
			view.parts = append(view.parts, readEntity(t, p.Header, p))
		}
		return view
	}
	switch strings.ToLower(h.Get("Content-Transfer-Encoding")) {
	case "quoted-printable":
		data, err := io.ReadAll(quotedprintable.NewReader(r))
		if err != nil {
			t.Fatalf("qp: %v", err)
		}
		view.body = string(data)
	case "base64":
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.StdEncoding.DecodeString(stripSpace(string(data)))
		if err != nil {
			t.Fatalf("base64: %v", err)
		}
		view.body = string(decoded)
	default:
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		view.body = string(data)
	}
	return view
}

func mediaNames(parts []partView) []string {
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = p.media
	}
	return names
}

func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func TestConvertMissingFile(t *testing.T) {
	_, err := Convert(t.TempDir()+"/missing.pst", t.TempDir()+"/out.zip", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
