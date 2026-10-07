package convert

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// Mail is one RFC 5322 message produced from a PST item.
type Mail struct {
	Folder      string
	Subject     string
	FromName    string
	FromEmail   string
	To          string
	Cc          string
	Bcc         string
	MessageID   string
	InReplyTo   string
	References  string
	Date        time.Time
	Plain       string
	HTML        string
	Attachments []Attachment
}

// Attachment is a MIME part stored beside the message body.
type Attachment struct {
	Name        string
	ContentType string
	ContentID   string
	Inline      bool
	Data        []byte
}

// WriteEML writes m as a CRLF RFC 5322 message.
func WriteEML(w io.Writer, m Mail) error {
	contentType, body, err := buildBody(m)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	writeHeader(&buf, "From", formatAddress(m.FromName, m.FromEmail))
	writeHeader(&buf, "To", m.To)
	writeHeader(&buf, "Cc", m.Cc)
	writeHeader(&buf, "Bcc", m.Bcc)
	writeHeader(&buf, "Subject", encodeHeader(m.Subject))
	if !m.Date.IsZero() {
		writeHeader(&buf, "Date", m.Date.UTC().Format(time.RFC1123Z))
	}
	writeHeader(&buf, "Message-ID", m.MessageID)
	writeHeader(&buf, "In-Reply-To", m.InReplyTo)
	writeHeader(&buf, "References", m.References)
	writeHeader(&buf, "MIME-Version", "1.0")
	writeHeader(&buf, "Content-Type", contentType)
	if !strings.HasPrefix(contentType, "multipart/") {
		writeHeader(&buf, "Content-Transfer-Encoding", "quoted-printable")
	}
	buf.WriteString("\r\n")
	buf.Write(body)
	_, err = w.Write(buf.Bytes())
	return err
}

func writeHeader(buf *bytes.Buffer, key, value string) {
	value = cleanHeader(value)
	if value == "" {
		return
	}
	fmt.Fprintf(buf, "%s: %s\r\n", key, value)
}

// cleanHeader drops C0 controls that PST strings often keep, including a trailing NUL.
// CR and LF become spaces so a value cannot start a new header line.
func cleanHeader(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\r' || r == '\n':
			b.WriteByte(' ')
		case unicode.IsControl(r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func encodeHeader(s string) string {
	s = cleanHeader(s)
	if s == "" {
		return ""
	}
	if utf8.ValidString(s) && isASCII(s) {
		return s
	}
	return mime.BEncoding.Encode("utf-8", s)
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

func formatAddress(name, email string) string {
	name = cleanHeader(name)
	email = cleanHeader(email)
	if strings.Contains(email, "@") && !strings.ContainsAny(email, " \t<>") {
		return (&mail.Address{Name: name, Address: email}).String()
	}
	if name != "" {
		return encodeHeader(name)
	}
	return encodeHeader(email)
}

func buildBody(m Mail) (string, []byte, error) {
	plain, html := m.Plain, m.HTML
	if strings.TrimSpace(html) == "" {
		html = ""
	}
	if html == "" && looksLikeHTML(plain) {
		html = plain
		plain = ""
	}

	var texts []bodyPart
	if plain != "" || (html == "" && len(m.Attachments) == 0) {
		texts = append(texts, textPart(false, plain))
	}
	if html != "" {
		texts = append(texts, textPart(true, html))
	}
	root, err := joinParts(texts, "alternative")
	if err != nil {
		return "", nil, err
	}

	// cid: targets belong beside the HTML. Anything else stays a normal
	// attachment so a client does not hide a file the body never shows.
	related, regular := splitAttachments(html, m.Attachments)
	if len(related) > 0 && root.contentType != "" {
		parts := make([]bodyPart, 0, 1+len(related))
		parts = append(parts, root)
		for i, att := range related {
			parts = append(parts, attachmentPart(att, i))
		}
		ctype, data, err := writeMultipart("related", parts)
		if err != nil {
			return "", nil, err
		}
		root = bodyPart{contentType: ctype, data: data}
	}
	if len(regular) == 0 {
		if root.contentType == "" {
			return "text/plain; charset=utf-8", encodeQuotedPrintable(""), nil
		}
		return root.contentType, root.data, nil
	}
	parts := make([]bodyPart, 0, 1+len(regular))
	if root.contentType != "" {
		parts = append(parts, root)
	}
	for i, att := range regular {
		parts = append(parts, attachmentPart(att, i))
	}
	return writeMultipart("mixed", parts)
}

func textPart(html bool, s string) bodyPart {
	ctype := "text/plain; charset=utf-8"
	if html {
		ctype = "text/html; charset=utf-8"
	}
	return bodyPart{
		contentType: ctype,
		cte:         "quoted-printable",
		data:        encodeQuotedPrintable(s),
	}
}

func joinParts(parts []bodyPart, kind string) (bodyPart, error) {
	switch len(parts) {
	case 0:
		return bodyPart{}, nil
	case 1:
		return parts[0], nil
	default:
		ctype, data, err := writeMultipart(kind, parts)
		if err != nil {
			return bodyPart{}, err
		}
		return bodyPart{contentType: ctype, data: data}, nil
	}
}

func splitAttachments(html string, atts []Attachment) (related, regular []Attachment) {
	for _, att := range atts {
		if htmlRefers(html, att) {
			att.Inline = true
			if strings.Trim(cleanHeader(att.ContentID), "<>") == "" {
				if name := cleanHeader(att.Name); name != "" {
					att.ContentID = name
				}
			}
			related = append(related, att)
			continue
		}
		att.Inline = false
		att.ContentID = ""
		regular = append(regular, att)
	}
	return related, regular
}

func htmlRefers(html string, att Attachment) bool {
	if strings.TrimSpace(html) == "" {
		return false
	}
	lower := strings.ToLower(html)
	var ids []string
	if cid := strings.ToLower(strings.Trim(cleanHeader(att.ContentID), "<>")); cid != "" {
		ids = append(ids, cid)
	}
	if name := strings.ToLower(cleanHeader(att.Name)); name != "" {
		ids = append(ids, name)
	}
	for _, id := range ids {
		if containsCID(lower, "cid:"+id) || containsCID(lower, "cid:<"+id+">") {
			return true
		}
	}
	return false
}

// containsCID reports whether needle is a cid reference, not a prefix of a longer one.
func containsCID(s, needle string) bool {
	for from := 0; from < len(s); {
		i := strings.Index(s[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(needle)
		if end >= len(s) || !isCIDToken(s[end]) {
			return true
		}
		from = end
	}
	return false
}

func isCIDToken(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c > 127:
		return true
	default:
		return strings.ContainsRune("@._+-=", rune(c))
	}
}

type bodyPart struct {
	contentType string
	headers     []string
	cte         string
	data        []byte
}

var boundarySeq atomic.Uint64

func writeMultipart(kind string, parts []bodyPart) (string, []byte, error) {
	boundary := fmt.Sprintf("pst2eml_%s_%x", kind, boundarySeq.Add(1))
	var buf bytes.Buffer
	for _, part := range parts {
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", part.contentType)
		if part.cte != "" {
			fmt.Fprintf(&buf, "Content-Transfer-Encoding: %s\r\n", part.cte)
		}
		for _, header := range part.headers {
			buf.WriteString(header)
			buf.WriteString("\r\n")
		}
		buf.WriteString("\r\n")
		buf.Write(part.data)
		if len(part.data) == 0 || part.data[len(part.data)-1] != '\n' {
			buf.WriteString("\r\n")
		}
	}
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	params := map[string]string{"boundary": boundary}
	if kind == "related" && len(parts) > 0 {
		if media, _, err := mime.ParseMediaType(parts[0].contentType); err == nil && media != "" {
			params["type"] = media
		}
	}
	return mime.FormatMediaType("multipart/"+kind, params), buf.Bytes(), nil
}

func attachmentPart(att Attachment, index int) bodyPart {
	name := cleanHeader(att.Name)
	if name == "" {
		name = fmt.Sprintf("attachment-%d", index+1)
	}
	ctype := att.ContentType
	if parsed, _, err := mime.ParseMediaType(ctype); err == nil && parsed != "" {
		ctype = parsed
	} else {
		ctype = "application/octet-stream"
	}
	disp := "attachment"
	if att.Inline {
		disp = "inline"
	}
	headers := []string{
		"Content-Disposition: " + mime.FormatMediaType(disp, map[string]string{"filename": name}),
	}
	if cid := strings.Trim(cleanHeader(att.ContentID), "<>"); cid != "" {
		headers = append(headers, "Content-ID: <"+cid+">")
	}
	return bodyPart{
		contentType: mime.FormatMediaType(ctype, map[string]string{"name": name}),
		headers:     headers,
		cte:         "base64",
		data:        encodeBase64(att.Data),
	}
}

func encodeQuotedPrintable(s string) []byte {
	var buf bytes.Buffer
	qp := quotedprintable.NewWriter(&buf)
	_, _ = qp.Write([]byte(s))
	_ = qp.Close()
	return buf.Bytes()
}

func encodeBase64(data []byte) []byte {
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
	base64.StdEncoding.Encode(encoded, data)
	var buf bytes.Buffer
	for i := 0; i < len(encoded); i += 76 {
		end := min(i+76, len(encoded))
		buf.Write(encoded[i:end])
		buf.WriteString("\r\n")
	}
	return buf.Bytes()
}

func looksLikeHTML(s string) bool {
	trim := strings.TrimSpace(s)
	trim = strings.TrimPrefix(trim, "\uFEFF")
	if trim == "" {
		return false
	}
	head := trim
	if len(head) > 128 {
		head = head[:128]
	}
	lower := strings.ToLower(head)
	for _, prefix := range []string{
		"<!doctype",
		"<html",
		"<head",
		"<body",
		"<div",
		"<p>",
		"<p ",
		"<table",
		"<span",
		"<br",
		"<img",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
