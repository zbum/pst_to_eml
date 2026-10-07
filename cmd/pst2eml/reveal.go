package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/go-gui-org/go-gui/gui"
)

// offerReveal asks whether to show the ZIP, then opens it on yes.
// Tests replace it so they do not open a file manager.
var offerReveal = defaultOfferReveal

// startCommand launches a file manager. Tests replace it.
var startCommand = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the process. open and xdg-open exit immediately; explorer may not.
	go func() { _ = cmd.Wait() }()
	return nil
}

func defaultOfferReveal(w *gui.Window, zipPath string) {
	if w == nil || strings.TrimSpace(zipPath) == "" {
		return
	}
	w.NativeConfirmDialog(gui.NativeConfirmDialogCfg{
		Title: "변환 결과",
		Body:  revealPrompt(),
		Level: gui.AlertInfo,
		OnDone: func(r gui.NativeAlertResult, win *gui.Window) {
			openErr := completeReveal(r.Status, zipPath)
			app := gui.State[App](win)
			app.update(win, func() {
				app.asking = false
				if openErr != nil {
					app.appendLogLocked("", "결과를 열지 못했습니다: "+openErr.Error())
				}
			})
		},
	})
}

func revealPrompt() string {
	switch runtime.GOOS {
	case "darwin":
		return "변환 결과를 Finder에서 열어 줄까요?"
	case "windows":
		return "변환 결과를 탐색기에서 열어 줄까요?"
	default:
		return "변환 결과를 파일 관리자에서 열어 줄까요?"
	}
}

// completeReveal opens the ZIP when the user confirms. Any other answer
// is not an error.
func completeReveal(status gui.NativeDialogStatus, zipPath string) error {
	if status != gui.DialogOK {
		return nil
	}
	return revealResult(zipPath)
}

func revealResult(path string) error {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return errors.New("결과 경로가 비어 있습니다")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("결과 파일 없음: %w", err)
	}
	if info.IsDir() {
		return errors.New("결과 경로가 디렉터리입니다")
	}
	name, args, err := revealCommand(runtime.GOOS, abs)
	if err != nil {
		return err
	}
	if err := startCommand(name, args...); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// revealCommand builds the file-manager invocation that selects path.
// The path is one argument, never a shell string.
func revealCommand(goos, path string) (string, []string, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return "", nil, errors.New("결과 경로가 비어 있습니다")
	}
	switch goos {
	case "darwin":
		return "open", []string{"-R", "--", path}, nil
	case "windows":
		// explorer reads "/select," and the path as one token.
		return "explorer", []string{"/select," + path}, nil
	default:
		dir := filepath.Dir(path)
		if dir == "" {
			return "", nil, errors.New("결과 폴더를 알 수 없습니다")
		}
		return "xdg-open", []string{dir}, nil
	}
}
