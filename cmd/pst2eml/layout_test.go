package main

import (
	"path/filepath"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/backend/soft"
)

func TestMainViewAlignsBrowseWithField(t *testing.T) {
	gui.SetTheme(gui.ThemeLight)
	w := gui.NewWindow(gui.WindowCfg{
		State:     &App{status: "PST 파일을 선택하세요."},
		Title:     "PST → EML",
		Width:     760,
		Height:    560,
		MinWidth:  640,
		MinHeight: 480,
	})
	root := w.TestRender(mainView)
	if err := soft.RenderToPNG(w, 2, filepath.Join(t.TempDir(), "ui.png")); err != nil {
		t.Fatalf("screenshot: %v", err)
	}

	pst := findShape(root, "path-pst")
	browse := findShape(root, "browse-pst")
	zip := findShape(root, "path-zip")
	logBox := findShape(root, "log")
	if pst == nil || browse == nil || zip == nil || logBox == nil {
		t.Fatalf("missing shapes pst=%v browse=%v zip=%v log=%v", pst != nil, browse != nil, zip != nil, logBox != nil)
	}

	if !centerInside(browse, pst) {
		t.Fatalf("browse button Y=%v H=%v is outside PST field Y=%v H=%v", browse.Y, browse.Height, pst.Y, pst.Height)
	}
	if browse.X < pst.X+pst.Width {
		t.Fatalf("browse button X=%v overlaps field right edge %v", browse.X, pst.X+pst.Width)
	}
	zipBrowse := findShape(root, "browse-zip")
	if zipBrowse == nil || !centerInside(zipBrowse, zip) {
		t.Fatalf("ZIP browse button is not aligned with the ZIP field")
	}
	fieldGap := zip.Y - (pst.Y + pst.Height)
	if fieldGap > 48 {
		t.Fatalf("gap between PST and ZIP fields = %v", fieldGap)
	}
	if logBox.Height < 140 {
		t.Fatalf("log height = %v, want it to fill the window", logBox.Height)
	}
	bottomGap := root.Shape.Height - (logBox.Y + logBox.Height)
	if bottomGap > 40 {
		t.Fatalf("log bottom gap = %v", bottomGap)
	}
}

func TestMainViewKeepsProgressInside(t *testing.T) {
	gui.SetTheme(gui.ThemeLight)
	w := gui.NewWindow(gui.WindowCfg{
		State: &App{
			status:  "완료. EML 1통, 건너뜀 3, 실패 0.",
			log:     "Freebusy Data / LocalFreebusy",
			written: 1,
			pstPath: "/Users/nhn/Downloads/dist-list.pst",
			zipPath: "/Users/nhn/Downloads/dist-list.zip",
		},
		Title:     "PST → EML",
		Width:     760,
		Height:    560,
		MinWidth:  640,
		MinHeight: 480,
	})
	root := w.TestRender(mainView)
	if err := soft.RenderToPNG(w, 2, filepath.Join(t.TempDir(), "done.png")); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	bar := findShape(root, "progress")
	browse := findShape(root, "browse-pst")
	logBox := findShape(root, "log")
	if bar == nil || browse == nil || logBox == nil {
		t.Fatal("missing progress, browse, or log")
	}
	barRight := bar.X + bar.Width
	browseRight := browse.X + browse.Width
	if barRight > browseRight+1 || browseRight-barRight > 4 {
		t.Fatalf("progress right edge %v, browse right edge %v", barRight, browseRight)
	}
	if logBox.Height < 140 {
		t.Fatalf("log height = %v", logBox.Height)
	}
}

func TestUpdateFromConvertGoroutine(t *testing.T) {
	gui.SetTheme(gui.ThemeLight)
	app := &App{status: "idle"}
	w := gui.NewWindow(gui.WindowCfg{
		State:  app,
		Title:  "PST → EML",
		Width:  760,
		Height: 560,
	})
	w.TestRender(mainView)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 30 {
			app.update(w, func() {
				app.status = "변환 중"
				app.written = i + 1
				app.appendLogLocked("", "line")
			})
		}
	}()
	<-done
	w.FrameFn()
	got := app.snapshot()
	if got.written == 0 || got.status != "변환 중" {
		t.Fatalf("status=%q written=%d", got.status, got.written)
	}
}

func TestApplyPathShowsWithoutAnotherClick(t *testing.T) {
	gui.SetTheme(gui.ThemeLight)
	app := &App{status: "PST 파일을 선택하세요."}
	w := gui.NewWindow(gui.WindowCfg{
		State:  app,
		Title:  "PST → EML",
		Width:  760,
		Height: 560,
	})
	w.TestRender(mainView)
	const pst = "/tmp/mailbox.pst"
	applyPath(w, gui.NativeDialogResult{
		Status: gui.DialogOK,
		Paths:  []gui.AccessiblePath{{Path: pst}},
	}, false)
	if !w.FrameFn() {
		t.Fatal("choosing a file did not schedule a redraw")
	}
	root := w.TestRender(nil)
	if !layoutHasText(root, pst) {
		t.Fatal("PST path is not on screen")
	}
	if !layoutHasText(root, "/tmp/mailbox.zip") {
		t.Fatal("ZIP path is not on screen")
	}
}

func layoutHasText(l *gui.Layout, text string) bool {
	if l == nil || l.Shape == nil {
		return false
	}
	if l.Shape.TC != nil && l.Shape.TC.Text == text {
		return true
	}
	for i := range l.Children {
		if layoutHasText(&l.Children[i], text) {
			return true
		}
	}
	return false
}

func centerInside(inner, outer *gui.Shape) bool {
	mid := inner.Y + inner.Height/2
	return mid >= outer.Y && mid <= outer.Y+outer.Height
}

func findShape(l *gui.Layout, id string) *gui.Shape {
	if l == nil || l.Shape == nil {
		return nil
	}
	if l.Shape.ID == id {
		return l.Shape
	}
	for i := range l.Children {
		if s := findShape(&l.Children[i], id); s != nil {
			return s
		}
	}
	return nil
}
