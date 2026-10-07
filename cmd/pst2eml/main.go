package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/backend"
	"github.com/zbum/pst_to_eml/internal/convert"
)

const logLimit = 200

// App is the window state for the PST to EML converter.
type App struct {
	mu         sync.Mutex
	pstPath    string
	zipPath    string
	zipTouched bool
	running    bool
	closed     bool
	status     string
	log        string
	written    int
	failed     int
}

func main() {
	gui.SetTheme(gui.ThemeLight)
	w := gui.NewWindow(gui.WindowCfg{
		State:     &App{status: "PST 파일을 선택하세요."},
		Title:     "PST → EML",
		Width:     760,
		Height:    560,
		MinWidth:  640,
		MinHeight: 480,
		OnInit: func(w *gui.Window) {
			w.SetView(mainView)
		},
		OnCloseRequest: func(w *gui.Window) {
			app := gui.State[App](w)
			app.mu.Lock()
			app.closed = true
			app.mu.Unlock()
			w.Close()
		},
	})
	backend.Run(w)
}

type snap struct {
	pstPath string
	zipPath string
	running bool
	status  string
	log     string
	written int
	failed  int
}

func (a *App) snapshot() snap {
	a.mu.Lock()
	defer a.mu.Unlock()
	return snap{
		pstPath: a.pstPath,
		zipPath: a.zipPath,
		running: a.running,
		status:  a.status,
		log:     a.log,
		written: a.written,
		failed:  a.failed,
	}
}

// update applies fn on the frame thread. Lock from the convert goroutine
// trips go-gui's frame-lock panic while a frame is in progress.
func (a *App) update(w *gui.Window, fn func()) {
	if w == nil {
		return
	}
	w.QueueCommand(func(*gui.Window) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.closed {
			return
		}
		fn()
		w.InvalidateLayout()
	})
}

func mainView(w *gui.Window) gui.View {
	app := gui.State[App](w)
	s := app.snapshot()
	theme := gui.CurrentTheme()

	content := []gui.View{
		gui.Label("PST 파일을 EML ZIP으로 변환", theme.TextStyleDisplay),
		pathRow("PST", s.pstPath, "pst", "PST 파일", []gui.NativeFileFilter{{
			Name: "Outlook PST", Extensions: []string{"pst"},
		}}, false, s.running),
		pathRow("ZIP", s.zipPath, "zip", "저장할 ZIP", []gui.NativeFileFilter{{
			Name: "ZIP", Extensions: []string{"zip"},
		}}, true, s.running),
		gui.Button(gui.ButtonCfg{
			ID:       "convert",
			Label:    "변환",
			Variant:  gui.ButtonPrimary,
			Disabled: s.running || s.pstPath == "" || s.zipPath == "",
			OnClick: func(ctx gui.EventCtx) {
				startConvert(ctx.Window)
			},
		}),
	}
	if s.running || s.written > 0 || s.failed > 0 {
		content = append(content, progressView(s))
	}
	content = append(content,
		gui.Label(s.status, gui.TextStyle{}),
		gui.Label("로그", theme.TextStyleLabel),
		gui.Input(gui.InputCfg{
			ID:          "log",
			Mode:        gui.InputMultiline,
			ReadOnly:    true,
			Scrollable:  true,
			Text:        s.log,
			Sizing:      gui.FillFill,
			Placeholder: "변환 기록이 여기에 표시됩니다.",
		}),
	)

	return gui.Column(gui.ContainerCfg{
		Sizing:  gui.FillFill,
		Padding: gui.PaddingLarge,
		Spacing: gui.SpacingMedium,
		Content: content,
	})
}

func pathRow(label, value, id, title string, filters []gui.NativeFileFilter, save, running bool) gui.View {
	theme := gui.CurrentTheme()
	return gui.Column(gui.ContainerCfg{
		Sizing:     gui.FillFit,
		Padding:    gui.NoPadding,
		SizeBorder: gui.NoBorder,
		Spacing:    gui.SpacingSmall,
		Content: []gui.View{
			gui.Label(label, theme.TextStyleLabel),
			gui.Row(gui.ContainerCfg{
				Sizing:     gui.FillFit,
				Padding:    gui.NoPadding,
				SizeBorder: gui.NoBorder,
				Spacing:    gui.SpacingSmall,
				VAlign:     gui.VAlignMiddle,
				Content: []gui.View{
					gui.Input(gui.InputCfg{
						ID:       "path-" + id,
						Text:     value,
						Sizing:   gui.FillFit,
						ReadOnly: running,
						OnTextChanged: func(text string, ctx gui.EventCtx) {
							app := gui.State[App](ctx.Window)
							app.mu.Lock()
							if id == "pst" {
								app.pstPath = text
								if !app.zipTouched {
									app.zipPath = defaultZipPath(text)
								}
							} else {
								app.zipPath = text
								app.zipTouched = true
							}
							app.mu.Unlock()
						},
					}),
					gui.Button(gui.ButtonCfg{
						ID:       "browse-" + id,
						Label:    "찾기",
						Disabled: running,
						OnClick: func(ctx gui.EventCtx) {
							if save {
								ctx.Window.NativeSaveDialog(gui.NativeSaveDialogCfg{
									Title:            title,
									DefaultName:      filepath.Base(valueOr(value, "mailbox.zip")),
									DefaultExtension: "zip",
									Filters:          filters,
									OnDone: func(r gui.NativeDialogResult, win *gui.Window) {
										applyPath(win, r, true)
									},
								})
								return
							}
							ctx.Window.NativeOpenDialog(gui.NativeOpenDialogCfg{
								Title:   title,
								Filters: filters,
								OnDone: func(r gui.NativeDialogResult, win *gui.Window) {
									applyPath(win, r, false)
								},
							})
						},
					}),
				},
			}),
		},
	})
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func applyPath(w *gui.Window, r gui.NativeDialogResult, save bool) {
	if r.Status != gui.DialogOK {
		return
	}
	paths := r.PathStrings()
	if len(paths) == 0 {
		return
	}
	app := gui.State[App](w)
	app.mu.Lock()
	if save {
		app.zipPath = paths[0]
		app.zipTouched = true
	} else {
		app.pstPath = paths[0]
		if !app.zipTouched {
			app.zipPath = defaultZipPath(paths[0])
		}
	}
	app.mu.Unlock()
	// The file panel runs a nested loop and returns to an idle window.
	// Without a wake, the new path stays invisible until the next click.
	w.InvalidateLayout()
}

func progressView(s snap) gui.View {
	if !s.running && s.written == 0 && s.failed == 0 {
		return gui.Label("", gui.TextStyle{})
	}
	return gui.ProgressBar(gui.ProgressBarCfg{
		ID:         "progress",
		Sizing:     gui.FillFit,
		Indefinite: s.running,
		Percent:    1,
	})
}

func startConvert(w *gui.Window) {
	app := gui.State[App](w)
	s := app.snapshot()
	if s.running {
		return
	}
	if st, err := os.Stat(s.pstPath); err != nil || st.IsDir() {
		app.update(w, func() {
			app.status = "PST 파일을 찾을 수 없습니다."
		})
		return
	}
	if _, err := os.Stat(s.zipPath); err == nil {
		w.NativeConfirmDialog(gui.NativeConfirmDialogCfg{
			Title: "덮어쓰기",
			Body:  s.zipPath + " 파일이 이미 있습니다. 덮어쓸까요?",
			Level: gui.AlertWarning,
			OnDone: func(r gui.NativeAlertResult, win *gui.Window) {
				if r.Status == gui.DialogOK {
					runConvert(win)
				}
			},
		})
		return
	}
	runConvert(w)
}

func runConvert(w *gui.Window) {
	app := gui.State[App](w)
	app.mu.Lock()
	if app.closed || app.running {
		app.mu.Unlock()
		return
	}
	pstPath, zipPath := app.pstPath, app.zipPath
	app.running = true
	app.written = 0
	app.failed = 0
	app.log = ""
	app.status = "변환 중..."
	app.mu.Unlock()
	w.QueueCommand(func(*gui.Window) { w.InvalidateLayout() })

	go func() {
		res, err := convert.Convert(pstPath, zipPath, func(p convert.Progress) {
			app.update(w, func() {
				app.written = p.Written
				app.failed = p.Failed
				app.status = fmt.Sprintf("변환 중: %d통", p.Written)
				if p.Subject != "" {
					app.appendLogLocked(p.Folder, p.Subject)
				}
			})
		})
		app.update(w, func() {
			app.running = false
			app.written = res.Written
			app.failed = res.Failed
			for _, problem := range res.Problems {
				app.appendLogLocked("", problem)
			}
			if err != nil {
				app.status = "실패: " + err.Error()
				app.appendLogLocked("", err.Error())
				return
			}
			app.status = fmt.Sprintf("완료. EML %d통, 건너뜀 %d, 실패 %d. %s", res.Written, res.Skipped, res.Failed, zipPath)
		})
	}()
}

func (a *App) appendLogLocked(folder, line string) {
	if folder != "" {
		line = folder + " / " + line
	}
	if a.log == "" {
		a.log = line
	} else {
		a.log += "\n" + line
	}
	parts := strings.Split(a.log, "\n")
	if len(parts) > logLimit {
		a.log = strings.Join(parts[len(parts)-logLimit:], "\n")
	}
}

func defaultZipPath(pstPath string) string {
	ext := filepath.Ext(pstPath)
	base := strings.TrimSuffix(pstPath, ext)
	if base == "" || base == pstPath {
		return pstPath + ".zip"
	}
	return base + ".zip"
}
