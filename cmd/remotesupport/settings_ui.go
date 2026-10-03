//go:build windows

package main

import (
	"strings"
)

func populateSettingsControls() {
	cfg := currentSettings()
	if state.settingsEndpoint != 0 {
		setText(state.settingsEndpoint, cfg.SignalingURL)
	}
	if state.settingsQuality != 0 {
		idx := 0
		if cfg.Quality == 76 {
			idx = 1
		}
		sendMessage.Call(state.settingsQuality, 0x014E, uintptr(idx), 0)
	}
	if state.settingsResolution != 0 {
		idx := 0
		switch cfg.MaxHeight {
		case 1080:
			idx = 1
		case 720:
			idx = 2
		case 480:
			idx = 3
		}
		sendMessage.Call(state.settingsResolution, 0x014E, uintptr(idx), 0)
	}
	if state.settingsIdle != 0 {
		idx := 0
		switch cfg.IdleWarnMin {
		case 10:
			idx = 1
		case 20:
			idx = 2
		case 30:
			idx = 3
		}
		sendMessage.Call(state.settingsIdle, 0x014E, uintptr(idx), 0)
	}
	if state.settingsIdleAction != 0 {
		idx := 0
		if cfg.IdleClose {
			idx = 1
		}
		sendMessage.Call(state.settingsIdleAction, 0x014E, uintptr(idx), 0)
	}
	if state.settingsInternet != 0 {
		idx := 0
		if cfg.InternetCode {
			idx = 1
		}
		sendMessage.Call(state.settingsInternet, 0x014E, uintptr(idx), 0)
	}
	if state.settingsTurnURL != 0 {
		setText(state.settingsTurnURL, cfg.TurnURL)
	}
	if state.settingsTurnUser != 0 {
		setText(state.settingsTurnUser, cfg.TurnUser)
	}
	if state.settingsTurnPass != 0 {
		setText(state.settingsTurnPass, cfg.TurnPass)
	}
}

func showSettings() {
	clearChildren()
	state.mu.Lock()
	state.mode = 10
	state.mu.Unlock()
	populateSettingsControls()
	layoutUI()
	invalidateRect.Call(state.hwnd, 0, 1)
}

func showAbout() {
	clearChildren()
	state.mu.Lock()
	state.mode = 11
	state.mu.Unlock()
	if state.aboutTheme != 0 {
		sendMessage.Call(state.aboutTheme, 0x014E, uintptr(themeIndex(currentSettings().ThemePreset)), 0) // CB_SETCURSEL
		show(state.aboutTheme)
	}
	show(state.aboutUpdate)
	layoutUI()
	invalidateRect.Call(state.hwnd, 0, 1)
}

func saveSettingsFromUI() {
	cfg := currentSettings()
	if state.settingsEndpoint != 0 {
		cfg.SignalingURL = strings.TrimSpace(getText(state.settingsEndpoint))
	}
	if state.settingsQuality != 0 {
		r, _, _ := sendMessage.Call(state.settingsQuality, 0x0147, 0, 0)
		if r == 1 {
			cfg.Quality = 76
		} else {
			cfg.Quality = 88
		}
	}
	if state.settingsResolution != 0 {
		r, _, _ := sendMessage.Call(state.settingsResolution, 0x0147, 0, 0)
		switch r {
		case 1:
			cfg.MaxHeight = 1080
		case 2:
			cfg.MaxHeight = 720
		case 3:
			cfg.MaxHeight = 480
		default:
			cfg.MaxHeight = 0
		}
	}
	if state.settingsIdle != 0 {
		r, _, _ := sendMessage.Call(state.settingsIdle, 0x0147, 0, 0)
		switch r {
		case 1:
			cfg.IdleWarnMin = 10
		case 2:
			cfg.IdleWarnMin = 20
		case 3:
			cfg.IdleWarnMin = 30
		default:
			cfg.IdleWarnMin = 0
		}
	}
	if state.settingsIdleAction != 0 {
		r, _, _ := sendMessage.Call(state.settingsIdleAction, 0x0147, 0, 0)
		cfg.IdleClose = r == 1
	}
	if state.settingsInternet != 0 {
		r, _, _ := sendMessage.Call(state.settingsInternet, 0x0147, 0, 0)
		cfg.InternetCode = r == 1
	}
	if state.settingsTurnURL != 0 {
		cfg.TurnURL = strings.TrimSpace(getText(state.settingsTurnURL))
	}
	if state.settingsTurnUser != 0 {
		cfg.TurnUser = strings.TrimSpace(getText(state.settingsTurnUser))
	}
	if state.settingsTurnPass != 0 {
		cfg.TurnPass = getText(state.settingsTurnPass)
	}
	settingsMu.Lock()
	appCfg = cfg
	settingsMu.Unlock()
	saveSettings()
	captureMaxHeight.Store(int32(cfg.MaxHeight))
	setStatus("Ayarlar kaydedildi.")
	invalidateRect.Call(state.hwnd, 0, 1)
}

func resetSettingsUI() {
	settingsMu.Lock()
	appCfg = appSettings{Quality: 88, MaxHeight: 0, InputEnabled: true, IdleWarnMin: 10, IdleClose: false}
	settingsMu.Unlock()
	saveSettings()
	populateSettingsControls()
	captureMaxHeight.Store(0)
	setStatus("Ayarlar varsayılan değerlere döndürüldü.")
}
