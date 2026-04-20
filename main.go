package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	qrcode "github.com/skip2/go-qrcode"
)

// noWindow prevents subprocess console windows from flashing on screen.
var noWindow = &syscall.SysProcAttr{HideWindow: true}

// ─── Worker state ─────────────────────────────────────────────────────────────

type WorkerState struct {
	Label   string // "folder/5.mp4"
	JobNum  string // "3/12"
	Pct     float64
	Encoder string // "GPU" | "CPU" | ""
	Idle    bool
}

// ─── App state ────────────────────────────────────────────────────────────────

type CapcutApp struct {
	win        *app.Window
	projectDir string
	outputDir  string

	logLines []string
	logMu    sync.Mutex

	exporting bool
	exportMu  sync.Mutex

	workers   []WorkerState
	workersMu sync.Mutex

	jobsDone  int32
	jobsTotal int32
}

func (a *CapcutApp) appendLog(msg string) {
	a.logMu.Lock()
	a.logLines = append(a.logLines, msg)
	if len(a.logLines) > 10000 {
		a.logLines = a.logLines[len(a.logLines)-10000:]
	}
	a.logMu.Unlock()
	a.win.Invalidate()
}

func (a *CapcutApp) clearLog() {
	a.logMu.Lock()
	a.logLines = nil
	a.logMu.Unlock()
}

func (a *CapcutApp) getLogLines() []string {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	cp := make([]string, len(a.logLines))
	copy(cp, a.logLines)
	return cp
}

func (a *CapcutApp) startExport(opts ExportOptions) {
	a.exportMu.Lock()
	if a.exporting {
		a.exportMu.Unlock()
		return
	}
	a.exporting = true
	a.exportMu.Unlock()

	SaveConfig(Config{ProjectDir: opts.ProjectDir, OutputDir: opts.OutputDir})

	// Initialize worker panel
	n := opts.Workers
	if n < 1 {
		n = 1
	}
	a.workersMu.Lock()
	a.workers = make([]WorkerState, n)
	for i := range a.workers {
		a.workers[i].Idle = true
	}
	a.workersMu.Unlock()

	// Reset progress counters
	atomic.StoreInt32(&a.jobsDone, 0)
	atomic.StoreInt32(&a.jobsTotal, 0)

	opts.Log = func(msg string) {
		a.appendLog(msg)
	}
	opts.WorkerUpdate = func(id int, label, jobNum string, pct float64, encoder string, idle bool) {
		a.workersMu.Lock()
		if id >= 1 && id <= len(a.workers) {
			a.workers[id-1] = WorkerState{Label: label, JobNum: jobNum, Pct: pct, Encoder: encoder, Idle: idle}
		}
		a.workersMu.Unlock()
		a.win.Invalidate()
	}
	opts.OnJobDone = func(done, total int) {
		atomic.StoreInt32(&a.jobsDone, int32(done))
		atomic.StoreInt32(&a.jobsTotal, int32(total))
		a.win.Invalidate()
	}

	go func() {
		defer func() {
			a.exportMu.Lock()
			a.exporting = false
			a.exportMu.Unlock()
			a.appendLog("────────────────────────────────")
			a.win.Invalidate()
		}()
		RunExport(opts)
	}()
}

// ─── UI ───────────────────────────────────────────────────────────────────────

type UI struct {
	cApp *CapcutApp
	th   *material.Theme

	widthEditor   widget.Editor
	heightEditor  widget.Editor
	fpsEditor     widget.Editor
	workersEditor widget.Editor
	trackEditor   widget.Editor

	projectBrowseBtn widget.Clickable
	outputBrowseBtn  widget.Clickable
	exportBtn        widget.Clickable
	clearLogBtn      widget.Clickable
	openOutBtn       widget.Clickable

	paypalCopyBtn widget.Clickable
	bmcCopyBtn    widget.Clickable

	useGPU widget.Bool
	dryRun widget.Bool

	logList widget.List

	paypalQR paint.ImageOp
	bmcQR    paint.ImageOp
}

func newUI(ca *CapcutApp) *UI {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))

	u := &UI{cApp: ca, th: th}
	u.widthEditor.SingleLine = true
	u.widthEditor.SetText("3840")
	u.heightEditor.SingleLine = true
	u.heightEditor.SetText("2160")
	u.fpsEditor.SingleLine = true
	u.fpsEditor.SetText("30")
	u.workersEditor.SingleLine = true
	u.workersEditor.SetText("4")
	u.trackEditor.SingleLine = true
	u.useGPU.Value = true
	u.logList.Axis = layout.Vertical

	if qr, err := qrcode.New("https://paypal.me/daovanhungblogger", qrcode.High); err == nil {
		u.paypalQR = paint.NewImageOp(qr.Image(220))
	}
	if qr, err := qrcode.New("https://buymeacoffee.com/gyftd43bs", qrcode.High); err == nil {
		u.bmcQR = paint.NewImageOp(qr.Image(220))
	}

	return u
}

// ─── Frame ────────────────────────────────────────────────────────────────────

func (u *UI) frame(gtx layout.Context) layout.Dimensions {
	paint.FillShape(gtx.Ops, color.NRGBA{R: 250, G: 250, B: 252, A: 255},
		clip.Rect{Max: gtx.Constraints.Max}.Op())

	return layout.UniformInset(unit.Dp(12)).Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(u.drawTitle),
				layout.Rigid(spacer(12)),
				layout.Rigid(u.drawDirs),
				layout.Rigid(spacer(10)),
				layout.Rigid(divider),
				layout.Rigid(spacer(10)),
				layout.Rigid(u.drawSettings),
				layout.Rigid(spacer(10)),
				layout.Rigid(u.drawExportRow),
				layout.Rigid(spacer(8)),
				layout.Rigid(u.drawWorkers),
				layout.Rigid(spacer(8)),
				layout.Rigid(divider),
				layout.Rigid(spacer(6)),
				layout.Flexed(1, u.drawLog),
				layout.Rigid(divider),
				layout.Rigid(spacer(6)),
				layout.Rigid(u.drawDonate),
				layout.Rigid(spacer(6)),
			)
		},
	)
}

func spacer(dp unit.Dp) layout.Widget {
	return layout.Spacer{Height: dp}.Layout
}

func divider(gtx layout.Context) layout.Dimensions {
	sz := gtx.Constraints.Max
	sz.Y = gtx.Dp(1)
	paint.FillShape(gtx.Ops, color.NRGBA{R: 210, G: 210, B: 210, A: 255},
		clip.Rect{Max: sz}.Op())
	return layout.Dimensions{Size: sz}
}

// ── Title ─────────────────────────────────────────────────────────────────────

func (u *UI) drawTitle(gtx layout.Context) layout.Dimensions {
	lbl := material.H5(u.th, "CapCut Export")
	lbl.Color = color.NRGBA{R: 30, G: 30, B: 30, A: 255}
	return lbl.Layout(gtx)
}

// ── Directory rows ────────────────────────────────────────────────────────────

func (u *UI) drawDirs(gtx layout.Context) layout.Dimensions {
	if u.projectBrowseBtn.Clicked(gtx) {
		go func() {
			initial := u.cApp.projectDir
			if initial == "" {
				initial = DefaultCapcutDir()
			}
			path, err := psFolderDialog("Select CapCut project folder", initial)
			if err == nil && path != "" {
				u.cApp.projectDir = path
				u.cApp.win.Invalidate()
			}
		}()
	}
	if u.outputBrowseBtn.Clicked(gtx) {
		go func() {
			path, err := psFolderDialog("Select output folder", u.cApp.outputDir)
			if err == nil && path != "" {
				u.cApp.outputDir = path
				u.cApp.win.Invalidate()
			}
		}()
	}

	muted := color.NRGBA{R: 100, G: 100, B: 100, A: 255}
	projectTxt := u.cApp.projectDir
	if projectTxt == "" {
		projectTxt = "(not selected)"
	}
	outputTxt := u.cApp.outputDir
	if outputTxt == "" {
		outputTxt = "(not selected)"
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body1(u.th, "Project:")
					lbl.Font.Weight = font.Bold
					return lbl.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(u.th, projectTxt)
					lbl.Color = muted
					lbl.MaxLines = 1
					return lbl.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Button(u.th, &u.projectBrowseBtn, "Browse...").Layout(gtx)
				}),
			)
		}),
		layout.Rigid(spacer(6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body1(u.th, "Output: ")
					lbl.Font.Weight = font.Bold
					return lbl.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(u.th, outputTxt)
					lbl.Color = muted
					lbl.MaxLines = 1
					return lbl.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Button(u.th, &u.outputBrowseBtn, "Browse...").Layout(gtx)
				}),
			)
		}),
	)
}

// ── Settings ──────────────────────────────────────────────────────────────────

func (u *UI) drawSettings(gtx layout.Context) layout.Dimensions {
	// Drain editor events
	for { if _, ok := u.widthEditor.Update(gtx); !ok { break } }
	for { if _, ok := u.heightEditor.Update(gtx); !ok { break } }
	for { if _, ok := u.fpsEditor.Update(gtx); !ok { break } }
	for { if _, ok := u.workersEditor.Update(gtx); !ok { break } }
	for { if _, ok := u.trackEditor.Update(gtx); !ok { break } }

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Row 1: Width / Height / FPS / Workers
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(labelW(u.th, "Width:")),
				layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
				layout.Rigid(editorBox(u.th, &u.widthEditor, "3840", 60)),
				layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
				layout.Rigid(labelW(u.th, "Height:")),
				layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
				layout.Rigid(editorBox(u.th, &u.heightEditor, "2160", 60)),
				layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
				layout.Rigid(labelW(u.th, "FPS:")),
				layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
				layout.Rigid(editorBox(u.th, &u.fpsEditor, "30", 50)),
				layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
				layout.Rigid(labelW(u.th, "Workers:")),
				layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
				layout.Rigid(editorBox(u.th, &u.workersEditor, "4", 40)),
			)
		}),
		layout.Rigid(spacer(6)),
		// Row 2: Track filter
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(labelW(u.th, "Track Filter:")),
				layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					ed := material.Editor(u.th, &u.trackEditor, "filter by track name (optional)")
					ed.TextSize = unit.Sp(14)
					return ed.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(spacer(6)),
		// Row 3: Checkboxes
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.CheckBox(u.th, &u.useGPU, "Use GPU (NVENC)").Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(24)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.CheckBox(u.th, &u.dryRun, "Dry Run").Layout(gtx)
				}),
			)
		}),
	)
}

func labelW(th *material.Theme, s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return material.Body1(th, s).Layout(gtx)
	}
}

func editorBox(th *material.Theme, ed *widget.Editor, hint string, width int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Dp(unit.Dp(width))
		gtx.Constraints.Max.X = gtx.Dp(unit.Dp(width))
		return material.Editor(th, ed, hint).Layout(gtx)
	}
}

// ── Export button ─────────────────────────────────────────────────────────────

func (u *UI) drawExportRow(gtx layout.Context) layout.Dimensions {
	u.cApp.exportMu.Lock()
	exporting := u.cApp.exporting
	u.cApp.exportMu.Unlock()

	if u.exportBtn.Clicked(gtx) && !exporting {
		if u.cApp.projectDir == "" || u.cApp.outputDir == "" {
			u.cApp.appendLog("[ERROR] Please select both project and output directories")
		} else {
			u.cApp.clearLog()
			opts := ExportOptions{
				ProjectDir: u.cApp.projectDir,
				OutputDir:  u.cApp.outputDir,
				Track:      strings.TrimSpace(u.trackEditor.Text()),
				Width:      parseIntStr(u.widthEditor.Text(), 3840),
				Height:     parseIntStr(u.heightEditor.Text(), 2160),
				FPS:        parseIntStr(u.fpsEditor.Text(), 30),
				Workers:    parseIntStr(u.workersEditor.Text(), 4),
				UseGPU:     u.useGPU.Value,
				DryRun:     u.dryRun.Value,
			}
			u.cApp.startExport(opts)
		}
	}

	if u.openOutBtn.Clicked(gtx) && u.cApp.outputDir != "" {
		dir := u.cApp.outputDir
		go func() {
			c := exec.Command("explorer.exe", dir)
			_ = c.Start()
		}()
	}

	btnLabel := "Export"
	if exporting {
		btnLabel = "Exporting..."
	}

	return layout.Flex{Alignment: layout.Middle, Spacing: layout.SpaceStart}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(200)
				btn := material.Button(u.th, &u.exportBtn, btnLabel)
				if exporting {
					btn.Background = color.NRGBA{R: 150, G: 150, B: 150, A: 255}
				} else {
					btn.Background = color.NRGBA{R: 0, G: 120, B: 212, A: 255}
				}
				return btn.Layout(gtx)
			})
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			btn := material.Button(u.th, &u.openOutBtn, "Open Output")
			btn.Background = color.NRGBA{R: 80, G: 150, B: 80, A: 255}
			return btn.Layout(gtx)
		}),
	)
}

// ── Log area ──────────────────────────────────────────────────────────────────

func (u *UI) drawLog(gtx layout.Context) layout.Dimensions {
	if u.clearLogBtn.Clicked(gtx) {
		u.cApp.clearLog()
	}

	lines := u.cApp.getLogLines()
	if len(lines) == 0 {
		lines = []string{"Ready. Select project and output directories, then click Export."}
	}

	wasAtEnd := !u.logList.Position.BeforeEnd

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Header
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body1(u.th, "Log Output")
					lbl.Font.Weight = font.Bold
					return lbl.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					btn := material.Button(u.th, &u.clearLogBtn, "Clear")
					btn.Background = color.NRGBA{R: 120, G: 120, B: 120, A: 255}
					return btn.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(spacer(4)),
		// Log content
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, color.NRGBA{R: 30, G: 30, B: 30, A: 255},
				clip.Rect{Max: gtx.Constraints.Max}.Op())

			dims := layout.UniformInset(unit.Dp(6)).Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					return material.List(u.th, &u.logList).Layout(gtx, len(lines),
						func(gtx layout.Context, index int) layout.Dimensions {
							lbl := material.Body2(u.th, lines[index])
							lbl.TextSize = unit.Sp(12)
							lbl.Color = logLineColor(lines[index])
							return lbl.Layout(gtx)
						},
					)
				},
			)

			if wasAtEnd && len(lines) > 1 {
				u.logList.Position.First = len(lines) - 1
				u.logList.Position.BeforeEnd = false
			}
			return dims
		}),
	)
}

func logLineColor(line string) color.NRGBA {
	switch {
	case strings.Contains(line, "[ERROR]") || strings.Contains(line, "[x]"):
		return color.NRGBA{R: 255, G: 100, B: 100, A: 255}
	case strings.Contains(line, "[!]"):
		return color.NRGBA{R: 255, G: 180, B: 80, A: 255}
	case strings.Contains(line, "Done:") || strings.Contains(line, "done"):
		return color.NRGBA{R: 100, G: 220, B: 100, A: 255}
	case strings.Contains(line, "Track:") || strings.Contains(line, "Encoder:") || strings.Contains(line, "Canvas:"):
		return color.NRGBA{R: 100, G: 180, B: 255, A: 255}
	default:
		return color.NRGBA{R: 200, G: 200, B: 210, A: 255}
	}
}

// ── Workers panel ─────────────────────────────────────────────────────────────

func (u *UI) drawWorkers(gtx layout.Context) layout.Dimensions {
	u.cApp.workersMu.Lock()
	workers := make([]WorkerState, len(u.cApp.workers))
	copy(workers, u.cApp.workers)
	u.cApp.workersMu.Unlock()

	if len(workers) == 0 {
		return layout.Dimensions{}
	}

	done := int(atomic.LoadInt32(&u.cApp.jobsDone))
	total := int(atomic.LoadInt32(&u.cApp.jobsTotal))

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Header: "Workers" label + overall progress bar + done/total
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body1(u.th, "Workers")
					lbl.Font.Weight = font.Bold
					lbl.Color = color.NRGBA{R: 50, G: 50, B: 50, A: 255}
					return lbl.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					var pct float32
					if total > 0 {
						pct = float32(done) / float32(total)
					}
					bar := material.ProgressBar(u.th, pct)
					bar.Color = color.NRGBA{R: 0, G: 120, B: 212, A: 255}
					bar.TrackColor = color.NRGBA{R: 210, G: 215, B: 220, A: 255}
					return bar.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(u.th, fmt.Sprintf("%d / %d clips", done, total))
					lbl.TextSize = unit.Sp(12)
					lbl.Color = color.NRGBA{R: 60, G: 60, B: 60, A: 255}
					return lbl.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(spacer(6)),
		// Worker rows
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var rows []layout.FlexChild
			for i, w := range workers {
				w := w
				i := i
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return u.drawWorkerRow(gtx, i+1, w)
				}))
				if i < len(workers)-1 {
					rows = append(rows, layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout))
				}
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		}),
	)
}

func (u *UI) drawWorkerRow(gtx layout.Context, id int, w WorkerState) layout.Dimensions {
	var encoderColor color.NRGBA
	encoderLabel := "Idle"
	if !w.Idle {
		switch w.Encoder {
		case "GPU":
			encoderColor = color.NRGBA{R: 20, G: 180, B: 100, A: 255}
			encoderLabel = "GPU"
		case "CPU":
			encoderColor = color.NRGBA{R: 60, G: 130, B: 210, A: 255}
			encoderLabel = "CPU"
		default:
			encoderColor = color.NRGBA{R: 210, G: 130, B: 40, A: 255}
			encoderLabel = w.Encoder
		}
	} else {
		encoderColor = color.NRGBA{R: 140, G: 140, B: 140, A: 255}
	}

	clipName := w.Label
	if clipName == "" {
		clipName = "—"
	}
	pctTxt := fmt.Sprintf("%3.0f%%", w.Pct*100)
	muted := color.NRGBA{R: 90, G: 90, B: 90, A: 255}

	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		// "Worker N" label
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(65)
			gtx.Constraints.Max.X = gtx.Dp(65)
			lbl := material.Body2(u.th, fmt.Sprintf("Worker %d", id))
			lbl.Color = muted
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		// Progress bar
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			bar := material.ProgressBar(u.th, float32(w.Pct))
			if !w.Idle {
				bar.Color = encoderColor
				bar.TrackColor = color.NRGBA{R: 210, G: 215, B: 220, A: 255}
			}
			return bar.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		// Percentage
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(34)
			gtx.Constraints.Max.X = gtx.Dp(34)
			lbl := material.Body2(u.th, pctTxt)
			lbl.TextSize = unit.Sp(12)
			lbl.Color = muted
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		// Clip label: folder/file.mp4
		layout.Flexed(2, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(u.th, clipName)
			lbl.MaxLines = 1
			lbl.TextSize = unit.Sp(12)
			lbl.Color = color.NRGBA{R: 30, G: 30, B: 30, A: 255}
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		// Job number "3/12" in blue
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if w.JobNum == "" {
				return layout.Dimensions{Size: image.Point{X: gtx.Dp(36)}}
			}
			gtx.Constraints.Min.X = gtx.Dp(36)
			lbl := material.Body2(u.th, w.JobNum)
			lbl.TextSize = unit.Sp(11)
			lbl.Color = color.NRGBA{R: 100, G: 100, B: 180, A: 255}
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		// Encoder badge
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(34)
			lbl := material.Body2(u.th, encoderLabel)
			lbl.Color = encoderColor
			lbl.TextSize = unit.Sp(12)
			lbl.Font.Weight = font.SemiBold
			return lbl.Layout(gtx)
		}),
	)
}

// ─── Donate panel ─────────────────────────────────────────────────────────────

func copyToClipboard(text string) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(`Set-Clipboard '%s'`, strings.ReplaceAll(text, `'`, `''`)))
	cmd.SysProcAttr = noWindow
	cmd.Run()
}

func (u *UI) drawDonate(gtx layout.Context) layout.Dimensions {
	// Handle copy clicks
	if u.bmcCopyBtn.Clicked(gtx) {
		copyToClipboard("https://buymeacoffee.com/gyftd43bs")
		u.cApp.appendLog("[Donate] Copied Buy Me a Coffee link!")
	}
	if u.paypalCopyBtn.Clicked(gtx) {
		copyToClipboard("https://paypal.me/daovanhungblogger")
		u.cApp.appendLog("[Donate] Copied PayPal link!")
	}

	muted := color.NRGBA{R: 110, G: 110, B: 110, A: 255}

	copyBtn := func(btn *widget.Clickable) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			b := material.Button(u.th, btn, "Copy link")
			b.TextSize = unit.Sp(10)
			b.Color = color.NRGBA{R: 80, G: 80, B: 80, A: 255}
			b.Background = color.NRGBA{R: 230, G: 230, B: 234, A: 255}
			b.Inset = layout.Inset{
				Top: unit.Dp(3), Bottom: unit.Dp(3),
				Left: unit.Dp(8), Right: unit.Dp(8),
			}
			return b.Layout(gtx)
		}
	}

	qrCard := func(imgOp paint.ImageOp, title string, titleColor color.NRGBA, btn *widget.Clickable) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return drawQRImage(gtx, imgOp, unit.Dp(100))
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(u.th, title)
					lbl.Font.Weight = font.SemiBold
					lbl.TextSize = unit.Sp(11)
					lbl.Color = titleColor
					return lbl.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
				layout.Rigid(copyBtn(btn)),
			)
		}
	}

	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(u.th, "Donate")
			lbl.Font.Weight = font.Bold
			lbl.TextSize = unit.Sp(11)
			lbl.Color = muted
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		layout.Rigid(qrCard(
			u.bmcQR,
			"Buy Me a Coffee",
			color.NRGBA{R: 180, G: 110, B: 0, A: 255},
			&u.bmcCopyBtn,
		)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(24)}.Layout),
		layout.Rigid(qrCard(
			u.paypalQR,
			"PayPal",
			color.NRGBA{R: 0, G: 60, B: 160, A: 255},
			&u.paypalCopyBtn,
		)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} }),
	)
}

func drawQRImage(gtx layout.Context, imgOp paint.ImageOp, sizeDp unit.Dp) layout.Dimensions {
	sz := gtx.Dp(sizeDp)
	// Add a white background first (QR codes are black on transparent by default)
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		clip.Rect{Max: image.Pt(sz, sz)}.Op())
	// Scale the QR image to the target dp size
	naturalSz := imgOp.Size()
	if naturalSz.X == 0 {
		return layout.Dimensions{Size: image.Pt(sz, sz)}
	}
	defer clip.Rect{Max: image.Pt(sz, sz)}.Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(image.Pt(sz, sz))
	return widget.Image{
		Src: imgOp,
		Fit: widget.Contain,
	}.Layout(gtx)
}

// ─── Native Windows folder dialog via PowerShell ─────────────────────────────

func psFolderDialog(desc, initialDir string) (string, error) {
	initialDir = strings.ReplaceAll(initialDir, `'`, `''`)
	ps := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = '%s'
$d.SelectedPath = '%s'
$d.ShowNewFolderButton = $true
if ($d.ShowDialog() -eq 'OK') { Write-Output $d.SelectedPath }`,
		strings.ReplaceAll(desc, `'`, `''`), initialDir)
	c := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", ps)
	c.SysProcAttr = noWindow
	out, err := c.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func parseIntStr(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	go func() {
		w := new(app.Window)
		w.Option(
			app.Title("CapCut Export"),
			app.Size(unit.Dp(800), unit.Dp(640)),
		)

		ca := &CapcutApp{win: w}

		cfg := LoadConfig()
		ca.projectDir = cfg.ProjectDir
		ca.outputDir = cfg.OutputDir

		ui := newUI(ca)

		var ops op.Ops
		for {
			switch e := w.Event().(type) {
			case app.DestroyEvent:
				os.Exit(0)
			case app.FrameEvent:
				gtx := app.NewContext(&ops, e)
				ui.frame(gtx)
				e.Frame(&ops)
			}
		}
	}()
	app.Main()
}
