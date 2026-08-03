package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"github.com/ysk/dacast-watchfolder/internal/applog"
	"github.com/ysk/dacast-watchfolder/internal/config"
	"github.com/ysk/dacast-watchfolder/internal/queue"
)

type Controller struct {
	Cfg       *config.Store
	Log       *applog.Logger
	OnStart   func() error
	OnStop    func()
	OnSave    func(config.Config) error
	IsRunning func() bool

	mainWindow  *walk.MainWindow
	apiEdit     *walk.LineEdit
	folderEdit  *walk.LineEdit
	statusLabel *walk.Label
	fileLabel   *walk.Label
	progress    *walk.ProgressBar
	logEdit     *walk.TextEdit
	startBtn    *walk.PushButton
	ni          *walk.NotifyIcon

	mu     sync.Mutex
	logBuf []string
}

func (c *Controller) Run() {
	cfg := c.Cfg.Get()
	c.logBuf = reverseCopy(c.Log.Tail())

	var startBtn *walk.PushButton

	err := MainWindow{
		AssignTo: &c.mainWindow,
		Title:    "Dacast Watchfolder",
		MinSize:  Size{Width: 680, Height: 480},
		Size:     Size{Width: 760, Height: 540},
		Layout:   VBox{},
		Children: []Widget{
			GroupBox{
				Title:  "Settings",
				Layout: Grid{Columns: 3},
				Children: []Widget{
					Label{Text: "API key"},
					LineEdit{
						AssignTo:     &c.apiEdit,
						Text:         cfg.APIKey,
						PasswordMode: true,
						ColumnSpan:   2,
					},
					Label{Text: "Watch folder"},
					LineEdit{
						AssignTo: &c.folderEdit,
						Text:     cfg.WatchFolder,
					},
					PushButton{
						Text: "Browse…",
						OnClicked: func() {
							dlg := new(walk.FileDialog)
							dlg.Title = "Select watch folder"
							dlg.FilePath = c.folderEdit.Text()
							ok, err := dlg.ShowBrowseFolder(c.mainWindow)
							if err != nil || !ok {
								return
							}
							_ = c.folderEdit.SetText(dlg.FilePath)
						},
					},
					PushButton{
						Text: "Save settings",
						OnClicked: func() {
							if err := c.saveSettings(); err != nil {
								walk.MsgBox(c.mainWindow, "Error", err.Error(), walk.MsgBoxIconError)
								return
							}
							walk.MsgBox(c.mainWindow, "Saved", "Settings saved.", walk.MsgBoxIconInformation)
						},
					},
					PushButton{
						AssignTo: &startBtn,
						Text:     "Start watching",
						OnClicked: func() {
							c.toggleStart()
						},
						ColumnSpan: 2,
					},
				},
			},
			GroupBox{
				Title:  "Status",
				Layout: VBox{},
				Children: []Widget{
					Label{AssignTo: &c.statusLabel, Text: "Idle"},
					Label{Text: "Current file"},
					Label{AssignTo: &c.fileLabel, Text: "—"},
					ProgressBar{
						AssignTo: &c.progress,
						MaxValue: 1000,
					},
				},
			},
			GroupBox{
				Title:  "Log",
				Layout: VBox{},
				Children: []Widget{
					TextEdit{
						AssignTo: &c.logEdit,
						ReadOnly: true,
						VScroll:  true,
						Text:     strings.Join(c.logBuf, "\r\n"),
					},
				},
			},
		},
	}.Create()
	if err != nil {
		panic(err)
	}
	c.startBtn = startBtn

	appIcon := loadAppIcon()
	if appIcon != nil {
		c.mainWindow.SetIcon(appIcon)
	}

	// Closing hides to tray instead of quitting.
	c.mainWindow.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		*canceled = true
		c.mainWindow.SetVisible(false)
	})

	ni, err := walk.NewNotifyIcon(c.mainWindow)
	if err != nil {
		panic(err)
	}
	c.ni = ni
	if appIcon != nil {
		_ = ni.SetIcon(appIcon)
	} else if icon, err := walk.NewIconFromResourceId(2); err == nil {
		_ = ni.SetIcon(icon)
	} else if fallback := walk.IconApplication(); fallback != nil {
		_ = ni.SetIcon(fallback)
	}
	_ = ni.SetToolTip("Dacast Watchfolder")
	_ = ni.SetVisible(true)

	openAction := walk.NewAction()
	_ = openAction.SetText("Open")
	openAction.Triggered().Attach(func() {
		c.mainWindow.SetVisible(true)
		_ = c.mainWindow.SetFocus()
	})
	_ = ni.ContextMenu().Actions().Add(openAction)

	toggleAction := walk.NewAction()
	_ = toggleAction.SetText("Start / Stop")
	toggleAction.Triggered().Attach(func() {
		c.toggleStart()
	})
	_ = ni.ContextMenu().Actions().Add(toggleAction)

	quitAction := walk.NewAction()
	_ = quitAction.SetText("Quit")
	quitAction.Triggered().Attach(func() {
		if c.OnStop != nil {
			c.OnStop()
		}
		_ = ni.Dispose()
		walk.App().Exit(0)
	})
	_ = ni.ContextMenu().Actions().Add(quitAction)

	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			c.mainWindow.SetVisible(true)
			_ = c.mainWindow.SetFocus()
		}
	})

	c.Log.OnLine(func(line string) {
		c.appendLog(line)
	})

	c.tryAutoStart()

	c.mainWindow.Show()
	c.mainWindow.Run()
}

func (c *Controller) tryAutoStart() {
	cfg := c.Cfg.Get()
	if strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.WatchFolder) == "" {
		c.Log.Infof("waiting for settings before watching")
		return
	}
	if c.OnStart == nil {
		return
	}
	if err := c.OnStart(); err != nil {
		c.Log.Errorf("auto-start watching failed: %v", err)
		return
	}
	c.setRunningUI(true)
	c.Log.Infof("auto-started watching")
}

func (c *Controller) saveSettings() error {
	newCfg := config.Config{
		APIKey:      strings.TrimSpace(c.apiEdit.Text()),
		WatchFolder: strings.TrimSpace(c.folderEdit.Text()),
	}
	if c.OnSave != nil {
		return c.OnSave(newCfg)
	}
	return nil
}

func (c *Controller) toggleStart() {
	if c.IsRunning != nil && c.IsRunning() {
		_ = c.statusLabel.SetText("Stopping…")
		if c.OnStop != nil {
			c.OnStop()
		}
		c.setRunningUI(false)
		return
	}
	if err := c.saveSettings(); err != nil {
		walk.MsgBox(c.mainWindow, "Error", err.Error(), walk.MsgBoxIconError)
		return
	}
	if c.OnStart != nil {
		if err := c.OnStart(); err != nil {
			walk.MsgBox(c.mainWindow, "Error", err.Error(), walk.MsgBoxIconError)
			return
		}
	}
	c.setRunningUI(true)
}

func (c *Controller) setRunningUI(running bool) {
	if c.startBtn == nil {
		return
	}
	if running {
		_ = c.startBtn.SetText("Stop watching")
		_ = c.statusLabel.SetText("Watching")
		if c.ni != nil {
			_ = c.ni.SetToolTip("Dacast Watchfolder — Watching")
		}
	} else {
		_ = c.startBtn.SetText("Start watching")
		_ = c.statusLabel.SetText("Idle")
		_ = c.fileLabel.SetText("—")
		c.progress.SetValue(0)
		if c.ni != nil {
			_ = c.ni.SetToolTip("Dacast Watchfolder — Idle")
		}
	}
}

func (c *Controller) appendLog(line string) {
	c.mu.Lock()
	// Newest lines at the top of the UI.
	c.logBuf = append([]string{line}, c.logBuf...)
	if len(c.logBuf) > 500 {
		c.logBuf = c.logBuf[:500]
	}
	text := strings.Join(c.logBuf, "\r\n")
	c.mu.Unlock()

	if c.mainWindow == nil || c.logEdit == nil {
		return
	}
	c.mainWindow.Synchronize(func() {
		_ = c.logEdit.SetText(text)
		c.logEdit.SetTextSelection(0, 0)
	})
}

func reverseCopy(in []string) []string {
	out := make([]string, len(in))
	for i := range in {
		out[len(in)-1-i] = in[i]
	}
	return out
}

func (c *Controller) UpdateProgress(ev queue.ProgressEvent) {
	if c.mainWindow == nil {
		return
	}
	c.mainWindow.Synchronize(func() {
		_ = c.fileLabel.SetText(filepath.Base(ev.Path))
		var pct float64
		if ev.Total > 0 {
			pct = float64(ev.Uploaded) / float64(ev.Total)
		}
		c.progress.SetValue(int(pct * 1000))
		_ = c.statusLabel.SetText(fmt.Sprintf("Uploading %d/%d parts (%.0f%%)", ev.Part, ev.Parts, pct*100))
		if c.ni != nil {
			_ = c.ni.SetToolTip(fmt.Sprintf("Uploading %.0f%% — %s", pct*100, filepath.Base(ev.Path)))
		}
	})
}

func (c *Controller) SetStatus(s string) {
	if c.mainWindow == nil || c.statusLabel == nil {
		return
	}
	c.mainWindow.Synchronize(func() {
		_ = c.statusLabel.SetText(s)
	})
}
