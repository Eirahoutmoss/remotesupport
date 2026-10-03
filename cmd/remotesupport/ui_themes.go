//go:build windows

package main

// User-selectable themes. The palette lives in runtime variables (state.go);
// a preset just overwrites them, recreates the solid brushes, and repaints.
// Colors are intuitive 0xRRGGBB; rgb() flips them to GDI's BGR at use sites.

type themePalette struct {
	bg, bg2, bar, nav, panel, panelHi, edit, border uint32
	text, muted, faint                              uint32
	accent, accentHi, accentDim, cyan               uint32
	success, danger, dangerHi, warn                 uint32
}

// themeOrder is the display order in the selector; themeKeys[i] <-> themeNames[i].
var themeKeys = []string{"gece", "sicak", "acik"}
var themeNames = []string{"Gece Mavisi", "Sıcak", "Aydınlık"}

var themePalettes = map[string]themePalette{
	// Gece Mavisi — refined cool navy with the brand blue (the classic look).
	"gece": {
		bg: 0x0A0E18, bg2: 0x0C1220, bar: 0x0C111C, nav: 0x070A12,
		panel: 0x151D2E, panelHi: 0x1D2740, edit: 0x060910, border: 0x243249,
		text: 0xF4F7FB, muted: 0x92A3BC, faint: 0x586981,
		accent: 0x3B8EFF, accentHi: 0x66AEFF, accentDim: 0x2563C4, cyan: 0x3FC6F5,
		success: 0x2BD488, danger: 0xF15460, dangerHi: 0xF56B77, warn: 0xF6AC33,
	},
	// Sıcak — amber/espresso, warm and inviting.
	"sicak": {
		bg: 0x14100A, bg2: 0x1B150D, bar: 0x18120B, nav: 0x100C07,
		panel: 0x241B12, panelHi: 0x312517, edit: 0x0E0A06, border: 0x3D2F1E,
		text: 0xF8F1E6, muted: 0xC2AC8E, faint: 0x8A7458,
		accent: 0xF5A623, accentHi: 0xFFBE4D, accentDim: 0xB97608, cyan: 0xFF8A5B,
		success: 0x7FB83E, danger: 0xEF5533, dangerHi: 0xF56B4C, warn: 0xFFC247,
	},
	// Aydınlık — soft light surfaces, dark text, brand blue accent.
	"acik": {
		bg: 0xEFF3FA, bg2: 0xE7EDF6, bar: 0xFFFFFF, nav: 0xE7EEF8,
		panel: 0xFFFFFF, panelHi: 0xE9F1FC, edit: 0xFFFFFF, border: 0xD3DEEC,
		text: 0x16243B, muted: 0x53647C, faint: 0x8695A9,
		accent: 0x2E7BFF, accentHi: 0x5A97FF, accentDim: 0x1C5FD0, cyan: 0x0E9AC0,
		success: 0x18A558, danger: 0xDC3545, dangerHi: 0xE4495A, warn: 0xD98A0B,
	},
}

// currentThemeKey tracks the applied preset so a no-op selection skips repaint.
var currentThemeKey = "sicak"

func normalizeThemeKey(key string) string {
	if _, ok := themePalettes[key]; ok {
		return key
	}
	return "sicak"
}

// themeIndex is the selector position for a key (0 if unknown).
func themeIndex(key string) int {
	for i, k := range themeKeys {
		if k == normalizeThemeKey(key) {
			return i
		}
	}
	return 0
}

// setThemeVars overwrites the palette variables. Safe to call before the window
// or brushes exist (startup), since it touches only the color variables.
func setThemeVars(key string) {
	key = normalizeThemeKey(key)
	p := themePalettes[key]
	uiBgColor, uiBgColor2, uiBarColor, uiNavColor = p.bg, p.bg2, p.bar, p.nav
	uiPanelColor, uiPanelHi, uiEditColor, uiBorderColor = p.panel, p.panelHi, p.edit, p.border
	uiTextColor, uiMutedColor, uiFaintColor = p.text, p.muted, p.faint
	uiAccentColor, uiAccentHi, uiAccentDim, uiCyanColor = p.accent, p.accentHi, p.accentDim, p.cyan
	uiSuccess, uiDanger, uiDangerHi, uiWarn = p.success, p.danger, p.dangerHi, p.warn
	currentThemeKey = key
}

// recreateThemeBrushes rebuilds the solid brushes from the current palette. The
// window class has no background brush (HbrBackground=0; WM_PAINT draws it), and
// the edit/panel brushes are queried fresh on each WM_CTLCOLOR, so swapping the
// handles and deleting the old ones is safe.
func recreateThemeBrushes() {
	old := []uintptr{uiBgBrush, uiPanelBrush, uiEditBrush}
	uiBgBrush, _, _ = createSolidBrush.Call(rgb(uiBgColor))
	uiPanelBrush, _, _ = createSolidBrush.Call(rgb(uiPanelColor))
	uiEditBrush, _, _ = createSolidBrush.Call(rgb(uiEditColor))
	for _, b := range old {
		if b != 0 {
			deleteObject.Call(b)
		}
	}
}

// onThemeSelect is fired by the Hakkında theme combo: apply + persist the pick.
func onThemeSelect() {
	if state.aboutTheme == 0 {
		return
	}
	r, _, _ := sendMessage.Call(state.aboutTheme, 0x0147, 0, 0) // CB_GETCURSEL
	i := int(r)
	if i < 0 || i >= len(themeKeys) {
		return
	}
	key := themeKeys[i]
	if key == currentThemeKey {
		return
	}
	applyThemePreset(key)
	cfg := currentSettings()
	cfg.ThemePreset = key
	settingsMu.Lock()
	appCfg = cfg
	settingsMu.Unlock()
	saveSettings()
}

// applyThemePreset switches the live theme and repaints. Used at runtime from
// the Hakkında selector. A no-op selection (same key) is ignored.
func applyThemePreset(key string) {
	key = normalizeThemeKey(key)
	if key == currentThemeKey && uiBgBrush != 0 {
		return
	}
	setThemeVars(key)
	recreateThemeBrushes()
	if state.hwnd != 0 {
		invalidateRect.Call(state.hwnd, 0, 1)
	}
}
