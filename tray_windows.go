//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os/exec"

	"github.com/energye/systray"
)

func hasGUI() bool { return true }

func runGUI() {
	systray.Run(onReady, onExit)
}

// --- Tray setup ---

var (
	relay  *Relay
	config Config

	mStatus    *systray.MenuItem
	mToggle    *systray.MenuItem
	mAutoStart *systray.MenuItem
)

func setUI(running bool, errMsg string) {
	if errMsg != "" {
		mStatus.SetTitle(fmt.Sprintf("Error: %s", errMsg))
		systray.SetIcon(iconRed)
		systray.SetTooltip("WG Relay - Error")
		mToggle.SetTitle("Start")
		return
	}
	if running {
		mStatus.SetTitle(relay.StatusLine(config))
		systray.SetIcon(iconGreen)
		systray.SetTooltip("WG Relay - " + relay.StatusLine(config))
		mToggle.SetTitle("Stop")
	} else {
		mStatus.SetTitle("Stopped")
		systray.SetIcon(iconRed)
		systray.SetTooltip("WG Relay - Stopped")
		mToggle.SetTitle("Start")
	}
}

func onReady() {
	relay = NewRelay()

	cfg, err := loadConfig()
	if err != nil {
		cfg = defaultConfig()
		_ = saveConfig(cfg)
		openConfigInEditor()
	}
	config = cfg

	systray.SetIcon(iconRed)
	systray.SetTooltip("WG Relay - Stopped")

	mStatus = systray.AddMenuItem("Stopped", "")
	mStatus.Disable()
	systray.AddSeparator()

	mToggle = systray.AddMenuItem("Start", "Start/stop the relay")
	mToggle.Click(func() {
		if relay.IsRunning() {
			relay.Stop()
			setUI(false, "")
		} else {
			if cfg, err := loadConfig(); err == nil {
				config = cfg
			}
			if err := relay.Start(config); err != nil {
				setUI(false, err.Error())
			} else {
				setUI(true, "")
			}
		}
	})

	mReload := systray.AddMenuItem("Reload Config", "Reload and restart if running")
	mReload.Click(func() {
		if cfg, err := loadConfig(); err == nil {
			config = cfg
			if relay.IsRunning() {
				relay.Stop()
				if err := relay.Start(config); err != nil {
					setUI(false, err.Error())
				} else {
					setUI(true, "")
				}
			}
		}
	})

	mEdit := systray.AddMenuItem("Edit Config", "Open config.json in Notepad")
	mEdit.Click(func() {
		openConfigInEditor()
	})

	systray.AddSeparator()

	mAutoStart = systray.AddMenuItemCheckbox("Auto Start", "Launch on Windows boot", config.AutoStart)
	mAutoStart.Click(func() {
		config.AutoStart = !config.AutoStart
		if config.AutoStart {
			mAutoStart.Check()
		} else {
			mAutoStart.Uncheck()
		}
		_ = saveConfig(config)
		setAutoStart(config.AutoStart)
	})

	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "")
	mQuit.Click(func() {
		systray.Quit()
	})

	// Auto-start relay if configured and ready
	if config.AutoStart {
		ok := (config.effectiveMode() == "client" && config.RemoteAddr != "") ||
			(config.effectiveMode() == "server" && config.ForwardAddr != "")
		if ok {
			if err := relay.Start(config); err != nil {
				log.Printf("auto-start failed: %v", err)
				setUI(false, err.Error())
			} else {
				setUI(true, "")
			}
		}
	}
}

func onExit() {
	if relay != nil {
		relay.Stop()
	}
}

// --- Windows platform helpers ---

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

// --- Icon generation ---

func makeIcon(r, g, b uint8) []byte {
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
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	return wrapICO(pngBuf.Bytes(), size)
}

func wrapICO(pngData []byte, size int) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	buf.WriteByte(byte(size))
	buf.WriteByte(byte(size))
	buf.WriteByte(0)
	buf.WriteByte(0)
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	binary.Write(&buf, binary.LittleEndian, uint32(len(pngData)))
	binary.Write(&buf, binary.LittleEndian, uint32(6+16))
	buf.Write(pngData)
	return buf.Bytes()
}

var (
	iconGreen = makeIcon(0x4C, 0xAF, 0x50)
	iconRed   = makeIcon(0xF4, 0x43, 0x36)
)
