package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/zbum/pst_to_eml/internal/convert"
)

func TestRevealCommandSelectsFile(t *testing.T) {
	const path = `/tmp/mailbox.zip`
	tests := []struct {
		goos string
		name string
		args []string
	}{
		{"darwin", "open", []string{"-R", "--", path}},
		{"windows", "explorer", []string{`/select,` + path}},
		{"linux", "xdg-open", []string{filepath.Dir(path)}},
	}
	for _, tt := range tests {
		name, args, err := revealCommand(tt.goos, path)
		if err != nil {
			t.Fatalf("%s: %v", tt.goos, err)
		}
		if name != tt.name || strings.Join(args, "\x00") != strings.Join(tt.args, "\x00") {
			t.Fatalf("%s: got %s %q, want %s %q", tt.goos, name, args, tt.name, tt.args)
		}
	}
}

func TestRevealCommandRejectsEmpty(t *testing.T) {
	if _, _, err := revealCommand("darwin", ""); err == nil {
		t.Fatal("empty path was accepted")
	}
	if _, _, err := revealCommand("darwin", "a\x00b"); err == nil {
		t.Fatal("path with NUL was accepted")
	}
}

func TestRevealResultStartsCommand(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "out.zip")
	if err := os.WriteFile(zipPath, []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotName string
	var gotArgs []string
	orig := startCommand
	t.Cleanup(func() { startCommand = orig })
	startCommand = func(name string, args ...string) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	}
	if err := revealResult(zipPath); err != nil {
		t.Fatal(err)
	}
	if gotName == "" || len(gotArgs) == 0 || gotArgs[len(gotArgs)-1] == "" {
		t.Fatalf("command = %s %q", gotName, gotArgs)
	}
	abs, err := filepath.Abs(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, "\x00")
	if !strings.Contains(joined, abs) && !strings.Contains(joined, filepath.Dir(abs)) {
		t.Fatalf("args %q do not mention %s", gotArgs, abs)
	}
}

func TestRevealResultMissingFile(t *testing.T) {
	orig := startCommand
	t.Cleanup(func() { startCommand = orig })
	startCommand = func(string, ...string) error {
		t.Fatal("file manager started for a missing file")
		return nil
	}
	err := revealResult(filepath.Join(t.TempDir(), "missing.zip"))
	if err == nil {
		t.Fatal("missing file was opened")
	}
}

func TestCompleteRevealSkipsUnlessYes(t *testing.T) {
	orig := startCommand
	t.Cleanup(func() { startCommand = orig })
	started := false
	startCommand = func(string, ...string) error {
		started = true
		return nil
	}
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	if err := os.WriteFile(zipPath, []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := completeReveal(gui.DialogCancel, zipPath); err != nil {
		t.Fatal(err)
	}
	if started {
		t.Fatal("cancel opened the file manager")
	}
	if err := completeReveal(gui.DialogOK, zipPath); err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("yes did not open the file manager")
	}
}

func TestFinishConvertAsksOnlyOnSuccess(t *testing.T) {
	gui.SetTheme(gui.ThemeLight)
	app := &App{
		status:  "변환 중...",
		running: true,
		pstPath: "/tmp/mailbox.pst",
		zipPath: "/tmp/mailbox.zip",
	}
	w := gui.NewWindow(gui.WindowCfg{State: app, Title: "PST → EML", Width: 760, Height: 560})
	w.TestRender(mainView)

	var offered []string
	orig := offerReveal
	t.Cleanup(func() { offerReveal = orig })
	offerReveal = func(_ *gui.Window, zipPath string) {
		offered = append(offered, zipPath)
	}

	finishConvert(app, w, convert.Result{}, errors.New("read pst: boom"), "/tmp/bad.zip")
	w.FrameFn()
	if len(offered) != 0 {
		t.Fatalf("offered on failure: %v", offered)
	}
	got := app.snapshot()
	if got.running || got.asking || !strings.HasPrefix(got.status, "실패:") {
		t.Fatalf("after failure: running=%v asking=%v status=%q", got.running, got.asking, got.status)
	}

	finishConvert(app, w, convert.Result{Written: 1}, nil, "  ")
	w.FrameFn()
	if len(offered) != 0 || app.snapshot().asking {
		t.Fatalf("empty result path offered=%v asking=%v", offered, app.snapshot().asking)
	}

	const zipPath = "/tmp/mailbox.zip"
	finishConvert(app, w, convert.Result{Written: 2, Skipped: 1}, nil, zipPath)
	w.FrameFn()
	if len(offered) != 1 || offered[0] != zipPath {
		t.Fatalf("offered = %v", offered)
	}
	got = app.snapshot()
	if got.running || !got.asking || !strings.HasPrefix(got.status, "완료.") {
		t.Fatalf("after success: running=%v asking=%v status=%q", got.running, got.asking, got.status)
	}
	root := w.TestRender(nil)
	button := findShape(root, "convert")
	if button == nil || !button.Disabled {
		t.Fatal("convert stays enabled while the reveal question is open")
	}
}

func TestDefaultOfferRevealCancelDoesNotOpen(t *testing.T) {
	gui.SetTheme(gui.ThemeLight)
	app := &App{asking: true, status: "완료."}
	w := gui.NewWindow(gui.WindowCfg{State: app, Title: "PST → EML", Width: 760, Height: 560})
	w.TestRender(mainView)

	dir := t.TempDir()
	zipPath := filepath.Join(dir, "out.zip")
	if err := os.WriteFile(zipPath, []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := startCommand
	t.Cleanup(func() { startCommand = orig })
	startCommand = func(string, ...string) error {
		t.Fatal("cancel opened the file manager")
		return nil
	}

	defaultOfferReveal(w, zipPath)
	// First frame runs the dialog. A nil platform answers with an error,
	// which is not yes, and queues the flag clear for the next frame.
	w.FrameFn()
	w.FrameFn()
	if app.snapshot().asking {
		t.Fatal("asking stayed set after the dialog closed")
	}
	if strings.Contains(app.snapshot().log, "열지 못했습니다") {
		t.Fatalf("cancel was logged as a failure: %q", app.snapshot().log)
	}
}
