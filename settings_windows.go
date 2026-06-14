//go:build windows

package main

import (
	"runtime"
	"strconv"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// showSettingsDialog opens a native Windows settings dialog.
// Returns the edited config and true if the user clicked Save.
func showSettingsDialog() (Config, bool) {
	cfg, _ := loadConfig()
	saved := false

	var dlg *walk.Dialog
	var (
		modeCombo, transportCombo       *walk.ComboBox
		listenAddrLE, listenPortLE      *walk.LineEdit
		remoteAddrLE, remotePortLE      *walk.LineEdit
		forwardAddrLE, forwardPortLE    *walk.LineEdit
		autoStartCB                     *walk.CheckBox
	)

	modes := []string{"client", "server"}
	transports := []string{"udp", "tcp"}

	modeIdx := 0
	if cfg.effectiveMode() == "server" {
		modeIdx = 1
	}
	transportIdx := 0
	if cfg.effectiveTransport() == "tcp" {
		transportIdx = 1
	}

	Dialog{
		AssignTo: &dlg,
		Title:    "WG Relay Settings",
		MinSize:  Size{Width: 420, Height: 400},
		Layout:   VBox{},
		Children: []Widget{
			GroupBox{
				Title:  "Connection",
				Layout: Grid{Columns: 2, Spacing: 8},
				Children: []Widget{
					Label{Text: "Mode:", TextAlignment: AlignFar},
					ComboBox{
						AssignTo:     &modeCombo,
						Model:        modes,
						CurrentIndex: modeIdx,
						Editable:     false,
					},
					Label{Text: "Transport:", TextAlignment: AlignFar},
					ComboBox{
						AssignTo:     &transportCombo,
						Model:        transports,
						CurrentIndex: transportIdx,
						Editable:     false,
					},
				},
			},
			GroupBox{
				Title:  "Listen",
				Layout: Grid{Columns: 2, Spacing: 8},
				Children: []Widget{
					Label{Text: "Address:", TextAlignment: AlignFar},
					LineEdit{AssignTo: &listenAddrLE, Text: cfg.ListenAddr},
					Label{Text: "Port:", TextAlignment: AlignFar},
					LineEdit{AssignTo: &listenPortLE, Text: strconv.Itoa(cfg.ListenPort)},
				},
			},
			GroupBox{
				Title:  "Remote (Client Mode)",
				Layout: Grid{Columns: 2, Spacing: 8},
				Children: []Widget{
					Label{Text: "Address:", TextAlignment: AlignFar},
					LineEdit{AssignTo: &remoteAddrLE, Text: cfg.RemoteAddr},
					Label{Text: "Port:", TextAlignment: AlignFar},
					LineEdit{AssignTo: &remotePortLE, Text: strconv.Itoa(cfg.RemotePort)},
				},
			},
			GroupBox{
				Title:  "Forward Target (Server Mode)",
				Layout: Grid{Columns: 2, Spacing: 8},
				Children: []Widget{
					Label{Text: "Address:", TextAlignment: AlignFar},
					LineEdit{AssignTo: &forwardAddrLE, Text: cfg.ForwardAddr},
					Label{Text: "Port:", TextAlignment: AlignFar},
					LineEdit{AssignTo: &forwardPortLE, Text: strconv.Itoa(cfg.ForwardPort)},
				},
			},
			VSpacer{Size: 4},
			CheckBox{
				AssignTo: &autoStartCB,
				Text:     "Start on Windows boot",
				Checked:  cfg.AutoStart,
			},
			VSpacer{Size: 8},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{
						Text: "Save",
						OnClicked: func() {
							cfg.Mode = modes[modeCombo.CurrentIndex()]
							cfg.Transport = transports[transportCombo.CurrentIndex()]
							cfg.ListenAddr = listenAddrLE.Text()
							cfg.ListenPort, _ = strconv.Atoi(listenPortLE.Text())
							cfg.RemoteAddr = remoteAddrLE.Text()
							cfg.RemotePort, _ = strconv.Atoi(remotePortLE.Text())
							cfg.ForwardAddr = forwardAddrLE.Text()
							cfg.ForwardPort, _ = strconv.Atoi(forwardPortLE.Text())
							cfg.AutoStart = autoStartCB.Checked()
							saved = true
							dlg.Accept()
						},
					},
					PushButton{
						Text:      "Cancel",
						OnClicked: func() { dlg.Cancel() },
					},
				},
			},
		},
	}.Run(nil)

	return cfg, saved
}

// openSettings spawns the settings dialog on a dedicated OS thread
// and handles config/relay updates on return.
func openSettings() {
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		newCfg, saved := showSettingsDialog()
		if !saved {
			return
		}
		_ = saveConfig(newCfg)
		config = newCfg

		setAutoStart(config.AutoStart)
		if config.AutoStart {
			mAutoStart.Check()
		} else {
			mAutoStart.Uncheck()
		}

		if relay.IsRunning() {
			relay.Stop()
			if err := relay.Start(config); err != nil {
				setUI(false, err.Error())
			} else {
				setUI(true, "")
			}
		}
	}()
}
