//go:build windows

package main

import (
	"fmt"
	"image"
	"image/color"
	"os/exec"
	"strconv"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

func hasGUI() bool { return true }

var (
	mw    *walk.MainWindow
	ni    *walk.NotifyIcon
	relay *Relay
	config Config

	statusLabel *walk.Label
	statsLabel  *walk.Label
	toggleBtn   *walk.PushButton

	modeCombo, transportCombo    *walk.ComboBox
	listenAddrLE, listenPortLE   *walk.LineEdit
	remoteAddrLE, remotePortLE   *walk.LineEdit
	forwardAddrLE, forwardPortLE *walk.LineEdit
	autoStartCB                  *walk.CheckBox

	trayToggleAction *walk.Action

	greenIcon, redIcon *walk.Icon
	quitRequested      bool

	modes      = []string{"client", "server"}
	transports = []string{"udp", "tcp"}
)

func runGUI() {
	relay = NewRelay()

	cfg, err := loadConfig()
	if err != nil {
		cfg = defaultConfig()
		_ = saveConfig(cfg)
	}
	config = cfg

	greenIcon = makeWalkIcon(0x4C, 0xAF, 0x50)
	redIcon = makeWalkIcon(0xF4, 0x43, 0x36)

	modeIdx := indexOf(cfg.effectiveMode(), modes)
	transportIdx := indexOf(cfg.effectiveTransport(), transports)

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "WG Relay",
		Visible:  false,
		MinSize:  Size{Width: 440, Height: 500},
		Size:     Size{Width: 440, Height: 500},
		Layout:   VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 8},
		Children: []Widget{
			GroupBox{
				Title:  "Status",
				Layout: VBox{Spacing: 4},
				Children: []Widget{
					Label{AssignTo: &statusLabel, Text: "\u25cf Stopped"},
					Label{AssignTo: &statsLabel, Text: ""},
					PushButton{AssignTo: &toggleBtn, Text: "Start", OnClicked: onToggle},
				},
			},
			GroupBox{
				Title:  "Connection",
				Layout: Grid{Columns: 4, Spacing: 6},
				Children: []Widget{
					Label{Text: "Mode:"},
					ComboBox{AssignTo: &modeCombo, Model: modes, CurrentIndex: modeIdx, Editable: false},
					Label{Text: "Transport:"},
					ComboBox{AssignTo: &transportCombo, Model: transports, CurrentIndex: transportIdx, Editable: false},
				},
			},
			GroupBox{
				Title:  "Listen",
				Layout: Grid{Columns: 4, Spacing: 6},
				Children: []Widget{
					Label{Text: "Address:"},
					LineEdit{AssignTo: &listenAddrLE, Text: cfg.ListenAddr},
					Label{Text: "Port:"},
					LineEdit{AssignTo: &listenPortLE, Text: itoa(cfg.ListenPort), MaxLength: 5},
				},
			},
			GroupBox{
				Title:  "Remote (Client Mode)",
				Layout: Grid{Columns: 4, Spacing: 6},
				Children: []Widget{
					Label{Text: "Address:"},
					LineEdit{AssignTo: &remoteAddrLE, Text: cfg.RemoteAddr},
					Label{Text: "Port:"},
					LineEdit{AssignTo: &remotePortLE, Text: itoa(cfg.RemotePort), MaxLength: 5},
				},
			},
			GroupBox{
				Title:  "Forward Target (Server Mode)",
				Layout: Grid{Columns: 4, Spacing: 6},
				Children: []Widget{
					Label{Text: "Address:"},
					LineEdit{AssignTo: &forwardAddrLE, Text: cfg.ForwardAddr},
					Label{Text: "Port:"},
					LineEdit{AssignTo: &forwardPortLE, Text: itoa(cfg.ForwardPort), MaxLength: 5},
				},
			},
			Composite{
				Layout: HBox{Spacing: 12},
				Children: []Widget{
					CheckBox{AssignTo: &autoStartCB, Text: "Auto Start", Checked: cfg.AutoStart},
					HSpacer{},
					PushButton{Text: "Apply", OnClicked: onApply},
				},
			},
		},
	}).Create(); err != nil {
		panic(err)
	}

	setupTray()

	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !quitRequested {
			*canceled = true
			mw.Hide()
		}
	})

	// Live stats
	go func() {
		for range time.Tick(time.Second) {
			mw.Synchronize(func() {
				if relay.IsRunning() {
					statsLabel.SetText(fmt.Sprintf(
						"\u2191 %s   \u2193 %s",
						humanBytes(relay.BytesSent.Load()),
						humanBytes(relay.BytesRecv.Load())))
				}
			})
		}
	}()

	// Auto-start relay
	if config.AutoStart && configReady(config) {
		doStart()
	}

	// First run: show window so user can configure
	if config.RemoteAddr == "" && config.effectiveMode() == "client" {
		mw.Show()
	}

	mw.Run()

	// Cleanup
	if ni != nil {
		ni.Dispose()
	}
	relay.Stop()
}

// --- Tray icon ---

func setupTray() {
	var err error
	ni, err = walk.NewNotifyIcon(mw)
	if err != nil {
		return
	}
	ni.SetIcon(redIcon)
	ni.SetToolTip("WG Relay - Stopped")
	ni.SetVisible(true)

	ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			toggleWindow()
		}
	})

	showAction := walk.NewAction()
	showAction.SetText("Show")
	showAction.Triggered().Attach(func() { showWindow() })

	trayToggleAction = walk.NewAction()
	trayToggleAction.SetText("Start")
	trayToggleAction.Triggered().Attach(func() { onToggle() })

	quitAction := walk.NewAction()
	quitAction.SetText("Quit")
	quitAction.Triggered().Attach(func() {
		quitRequested = true
		mw.Close()
	})

	ni.ContextMenu().Actions().Add(showAction)
	ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	ni.ContextMenu().Actions().Add(trayToggleAction)
	ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	ni.ContextMenu().Actions().Add(quitAction)
}

func toggleWindow() {
	if mw.Visible() {
		mw.Hide()
	} else {
		showWindow()
	}
}

func showWindow() {
	mw.Show()
	win.SetForegroundWindow(mw.Handle())
}

// --- Event handlers ---

func onToggle() {
	if relay.IsRunning() {
		doStop()
	} else {
		readUI()
		doStart()
	}
}

func doStart() {
	if err := relay.Start(config); err != nil {
		statusLabel.SetText("\u25cf Error: " + err.Error())
		return
	}
	statusLabel.SetText("\u25cf Running   " + relay.StatusLine(config))
	statsLabel.SetText("")
	toggleBtn.SetText("Stop")
	trayToggleAction.SetText("Stop")
	ni.SetIcon(greenIcon)
	ni.SetToolTip("WG Relay - Running")
	setSettingsEnabled(false)
}

func doStop() {
	relay.Stop()
	statusLabel.SetText("\u25cf Stopped")
	statsLabel.SetText("")
	toggleBtn.SetText("Start")
	trayToggleAction.SetText("Start")
	ni.SetIcon(redIcon)
	ni.SetToolTip("WG Relay - Stopped")
	setSettingsEnabled(true)
}

func onApply() {
	readUI()
	_ = saveConfig(config)
	setAutoStart(config.AutoStart)
	if relay.IsRunning() {
		doStop()
		doStart()
	}
}

func readUI() {
	config.Mode = modes[modeCombo.CurrentIndex()]
	config.Transport = transports[transportCombo.CurrentIndex()]
	config.ListenAddr = listenAddrLE.Text()
	config.ListenPort, _ = strconv.Atoi(listenPortLE.Text())
	config.RemoteAddr = remoteAddrLE.Text()
	config.RemotePort, _ = strconv.Atoi(remotePortLE.Text())
	config.ForwardAddr = forwardAddrLE.Text()
	config.ForwardPort, _ = strconv.Atoi(forwardPortLE.Text())
	config.AutoStart = autoStartCB.Checked()
}

func setSettingsEnabled(enabled bool) {
	modeCombo.SetEnabled(enabled)
	transportCombo.SetEnabled(enabled)
	listenAddrLE.SetEnabled(enabled)
	listenPortLE.SetEnabled(enabled)
	remoteAddrLE.SetEnabled(enabled)
	remotePortLE.SetEnabled(enabled)
	forwardAddrLE.SetEnabled(enabled)
	forwardPortLE.SetEnabled(enabled)
}

// --- Platform helpers ---

func setAutoStart(enable bool) {
	exePath, err := exec.LookPath("wg-relay.exe")
	if err != nil {
		return
	}
	if enable {
		exec.Command("reg", "add",
			`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,
			"/v", "WGRelay", "/t", "REG_SZ", "/d", exePath, "/f").Run()
	} else {
		exec.Command("reg", "delete",
			`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,
			"/v", "WGRelay", "/f").Run()
	}
}

func openConfigInEditor() {
	exec.Command("notepad.exe", configPath()).Start()
}

// --- Helpers ---

func configReady(cfg Config) bool {
	if cfg.effectiveMode() == "client" {
		return cfg.RemoteAddr != ""
	}
	return cfg.ForwardAddr != ""
}

func humanBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func makeWalkIcon(r, g, b uint8) *walk.Icon {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size)/2, float64(size)/2
	radius := float64(size)/2 - 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - cx + 0.5
			dy := float64(y) - cy + 0.5
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
			}
		}
	}
	icon, _ := walk.NewIconFromImage(img)
	return icon
}

func indexOf(s string, list []string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return 0
}

func itoa(n int) string { return strconv.Itoa(n) }
