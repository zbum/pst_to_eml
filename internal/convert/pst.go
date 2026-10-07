package convert

import (
	"archive/zip"
	"bytes"
	"cmp"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	charsets "github.com/emersion/go-message/charset"
	pst "github.com/mooijtech/go-pst/v6/pkg"
	"github.com/mooijtech/go-pst/v6/pkg/properties"
	"github.com/rotisserie/eris"
	"golang.org/x/text/encoding"
)

func init() {
	// go-pst decodes non-UTF-8 bodies through the charset registry.
	pst.ExtendCharsets(func(name string, enc encoding.Encoding) {
		charsets.RegisterEncoding(name, enc)
	})
}

// Progress is one step of a conversion, reported after each exported message.
type Progress struct {
	Folder  string
	Subject string
	Written int
	Skipped int
	Failed  int
}

// Result counts what Convert did. Problems holds the first few per-message failures.
type Result struct {
	Written  int
	Skipped  int
	Failed   int
	Problems []string
}

const maxProblems = 20

// Convert reads a PST file and writes one ZIP of EML messages.
// Contacts, appointments, and tasks are counted as skipped.
// report may be nil. A failure to open the PST or to write the ZIP is returned.
// A single unreadable message is counted in Result.Failed and conversion continues.
func Convert(pstPath, zipPath string, report func(Progress)) (Result, error) {
	f, err := os.Open(pstPath)
	if err != nil {
		return Result{}, fmt.Errorf("open pst: %w", err)
	}
	defer f.Close()

	pstFile, err := pst.New(f)
	if err != nil {
		return Result{}, fmt.Errorf("read pst: %w", err)
	}
	defer pstFile.Cleanup()

	out, err := os.Create(zipPath)
	if err != nil {
		return Result{}, fmt.Errorf("create zip: %w", err)
	}
	zw := zip.NewWriter(out)
	failed := true
	defer func() {
		if !failed {
			return
		}
		_ = zw.Close()
		_ = out.Close()
		_ = os.Remove(zipPath)
	}()

	// go-pst prints every message whose class is not an exact match.
	// Many PST strings include a trailing NUL, so "IPM.Note" misses and
	// each mail logs a line. Discard that noise for the walk.
	restoreStdout := discardStdout()
	defer restoreStdout()

	root, err := pstFile.GetRootFolder()
	if err != nil {
		return Result{}, fmt.Errorf("root folder: %w", err)
	}

	var res Result
	index := 0
	err = walkFolders(root, "", func(folder pst.Folder, folderPath string) error {
		if folder.MessageCount == 0 {
			return nil
		}
		it, err := folder.GetMessageIterator()
		if eris.Is(err, pst.ErrMessagesNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		for it.Next() {
			msg := it.Value()
			mail, ok, convErr := messageToMail(msg, folderPath, index+1)
			if convErr != nil {
				res.Failed++
				addProblem(&res, folderPath, convErr)
				reportProgress(report, folderPath, "", res)
				continue
			}
			if !ok {
				res.Skipped++
				continue
			}
			index++
			name := zipName(folderPath, index, mail.Subject)
			if err := writeEMLEntry(zw, name, mail); err != nil {
				return fmt.Errorf("write %s: %w", name, err)
			}
			res.Written++
			reportProgress(report, folderPath, mail.Subject, res)
		}
		if err := it.Err(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if err := zw.Close(); err != nil {
		return res, fmt.Errorf("close zip: %w", err)
	}
	if err := out.Close(); err != nil {
		return res, fmt.Errorf("close zip file: %w", err)
	}
	failed = false
	return res, nil
}

func discardStdout() func() {
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	os.Stdout = w
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, r)
		close(done)
	}()
	return func() {
		_ = w.Close()
		<-done
		os.Stdout = prev
		_ = r.Close()
	}
}

func reportProgress(report func(Progress), folder, subject string, res Result) {
	if report == nil {
		return
	}
	report(Progress{
		Folder:  folder,
		Subject: subject,
		Written: res.Written,
		Skipped: res.Skipped,
		Failed:  res.Failed,
	})
}

func addProblem(res *Result, folder string, err error) {
	if len(res.Problems) >= maxProblems {
		return
	}
	where := folder
	if where == "" {
		where = "(root)"
	}
	res.Problems = append(res.Problems, where+": "+err.Error())
}

func walkFolders(folder pst.Folder, folderPath string, fn func(pst.Folder, string) error) error {
	if err := fn(folder, folderPath); err != nil {
		return err
	}
	subs, err := folder.GetSubFolders()
	if err != nil {
		return fmt.Errorf("subfolders of %s: %w", folderPath, err)
	}
	for _, sub := range subs {
		child := joinFolder(folderPath, sub.Name)
		if err := walkFolders(sub, child, fn); err != nil {
			return err
		}
	}
	return nil
}

func messageToMail(msg *pst.Message, folderPath string, index int) (Mail, bool, error) {
	props, ok := msg.Properties.(*properties.Message)
	if !ok || props == nil {
		return Mail{}, false, nil
	}
	mail := Mail{
		Folder:     folderPath,
		Subject:    cleanHeader(cmp.Or(props.GetSubject(), props.GetInternetSubject())),
		FromName:   cleanHeader(cmp.Or(props.GetSentRepresentingName(), props.GetSenderName())),
		FromEmail:  firstSMTP(props.GetSmtpAddress(), props.GetSentRepresentingEmailAddress(), props.GetSenderEmailAddress()),
		To:         cleanHeader(props.GetDisplayTo()),
		Cc:         cleanHeader(props.GetDisplayCc()),
		Bcc:        cleanHeader(props.GetDisplayBcc()),
		MessageID:  cleanHeader(props.GetInternetMessageId()),
		InReplyTo:  cleanHeader(props.GetInReplyToId()),
		References: cleanHeader(props.GetInternetReferences()),
		Date:       mailDate(props.GetClientSubmitTime(), props.GetMessageDeliveryTime()),
	}
	fillBodies(msg, props, &mail)
	if mail.MessageID == "" {
		mail.MessageID = fmt.Sprintf("<pst-%d@pst-to-eml.local>", index)
	}
	atts, err := attachmentsOf(msg)
	if err != nil {
		return Mail{}, false, err
	}
	mail.Attachments = atts
	return mail, true, nil
}

func stripNUL(s string) string {
	if !strings.Contains(s, "\x00") {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

func firstSMTP(candidates ...string) string {
	var fallback string
	for _, c := range candidates {
		c = cleanHeader(c)
		if c == "" {
			continue
		}
		if fallback == "" {
			fallback = c
		}
		if strings.Contains(c, "@") {
			return c
		}
	}
	return fallback
}

func mailDate(submitNano, deliveryNano int64) time.Time {
	ns := submitNano
	if ns <= 0 {
		ns = deliveryNano
	}
	if ns <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

func attachmentsOf(msg *pst.Message) ([]Attachment, error) {
	it, err := msg.GetAttachmentIterator()
	if eris.Is(err, pst.ErrAttachmentsNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var atts []Attachment
	n := 0
	for it.Next() {
		n++
		att := it.Value()
		var buf bytes.Buffer
		if _, err := att.WriteTo(&buf); err != nil {
			return nil, fmt.Errorf("attachment %d: %w", n, err)
		}
		name := cmp.Or(att.GetAttachLongFilename(), att.GetAttachFilename(), fmt.Sprintf("attachment-%d", n))
		atts = append(atts, Attachment{
			Name:        name,
			ContentType: att.GetAttachMimeTag(),
			ContentID:   att.GetAttachContentId(),
			Data:        buf.Bytes(),
		})
	}
	if err := it.Err(); err != nil {
		return nil, err
	}
	return atts, nil
}

func writeEMLEntry(zw *zip.Writer, name string, mail Mail) error {
	hdr := &zip.FileHeader{
		Name:   name,
		Method: zip.Deflate,
	}
	if !mail.Date.IsZero() {
		hdr.Modified = mail.Date.UTC()
	}
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	return WriteEML(w, mail)
}

func zipName(folderPath string, index int, subject string) string {
	base := sanitizeSegment(subject)
	file := fmt.Sprintf("%06d.eml", index)
	if base != "" {
		file = fmt.Sprintf("%06d-%s.eml", index, base)
	}
	if folderPath == "" {
		return file
	}
	return path.Join(folderPath, file)
}

func joinFolder(parent, name string) string {
	seg := sanitizeSegment(name)
	if seg == "" {
		return parent
	}
	if parent == "" {
		return seg
	}
	return parent + "/" + seg
}

func sanitizeSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\r', '\n', 0:
			b.WriteByte('_')
		default:
			if r < 0x20 {
				b.WriteByte('_')
				continue
			}
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), " ._")
	const maxRunes = 80
	if utf8.RuneCountInString(out) > maxRunes {
		runes := []rune(out)
		out = strings.TrimRight(string(runes[:maxRunes]), " ._")
	}
	return out
}

// WriteZip writes mails into w as a ZIP of EML files. It exists so tests
// can check the archive without a PST file.
func WriteZip(w io.Writer, mails []Mail) error {
	zw := zip.NewWriter(w)
	for i, mail := range mails {
		name := zipName(mail.Folder, i+1, mail.Subject)
		if err := writeEMLEntry(zw, name, mail); err != nil {
			_ = zw.Close()
			return err
		}
	}
	return zw.Close()
}
