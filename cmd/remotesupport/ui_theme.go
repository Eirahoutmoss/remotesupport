//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

func utf16ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// rgb converts an intuitive 0xRRGGBB value to a Win32 COLORREF (0x00BBGGRR),
// which is the byte order GDI's CreateSolidBrush / SetTextColor expect.
func rgb(hex uint32) uintptr {
	r := (hex >> 16) & 0xFF
	g := (hex >> 8) & 0xFF
	b := hex & 0xFF
	return uintptr(b<<16 | g<<8 | r)
}

// mix blends two 0xRRGGBB colors by t in [0,1] and returns a COLORREF. Used for
// cheap vertical gradients (bands) and glow falloff without alpha blending.
func mix(a, b uint32, t float64) uintptr {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	ar, ag, ab := (a>>16)&0xFF, (a>>8)&0xFF, a&0xFF
	br, bg, bb := (b>>16)&0xFF, (b>>8)&0xFF, b&0xFF
	r := uint32(float64(ar) + (float64(br)-float64(ar))*t)
	g := uint32(float64(ag) + (float64(bg)-float64(ag))*t)
	bl := uint32(float64(ab) + (float64(bb)-float64(ab))*t)
	return uintptr(bl<<16 | g<<8 | r)
}

// fillBand paints a solid rectangle in a COLORREF (already byte-ordered).
func fillBand(hdc uintptr, x, y, w, h int, color uintptr) {
	if w <= 0 || h <= 0 {
		return
	}
	rc := rect{Left: int32(x), Top: int32(y), Right: int32(x + w), Bottom: int32(y + h)}
	brush, _, _ := createSolidBrush.Call(color)
	if brush != 0 {
		fillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), brush)
		deleteObject.Call(brush)
	}
}

// vGradient paints a vertical gradient from 0xRRGGBB top to bottom using n bands.
func vGradient(hdc uintptr, x, y, w, h int, top, bottom uint32, n int) {
	if h <= 0 || w <= 0 {
		return
	}
	if n < 1 {
		n = 1
	}
	step := float64(h) / float64(n)
	for i := 0; i < n; i++ {
		by := y + int(float64(i)*step)
		bh := y + int(float64(i+1)*step) - by
		if bh < 1 {
			bh = 1
		}
		fillBand(hdc, x, by, w, bh, mix(top, bottom, float64(i)/float64(n-1+1)))
	}
}

func makeUIFont(height int, weight int) uintptr {
	face := utf16ptr("Segoe UI")
	r, _, _ := createFont.Call(
		uintptr(int32(-height)), 0, 0, 0, uintptr(weight), 0, 0, 0,
		uintptr(1), 0, 0, 5, 0, uintptr(unsafe.Pointer(face)),
	)
	return r
}

func initUITheme() {
	recreateThemeBrushes()
	uiHeaderFont = makeUIFont(27, 700)   // page titles ("Uzaktan Destek")
	uiBrandFont = makeUIFont(20, 800)    // product wordmark
	uiTitleFont = makeUIFont(18, 700)    // card titles
	uiSubtitleFont = makeUIFont(15, 600) // section / row titles
	uiBodyFont = makeUIFont(13, 400)     // body text
	uiButtonFont = makeUIFont(13, 600)   // button labels
	uiLabelFont = makeUIFont(11, 700)    // ALL-CAPS section labels
	uiSmallFont = makeUIFont(11, 400)    // stats / captions
	uiCodeFont = makeUIFont(36, 800)     // big connection code
	uiEditFont = makeUIFont(22, 600)     // input fields
	uiIconFont = makeUIFont(20, 400)     // glyph icons
}

func setControlFont(hwnd, font uintptr) {
	if hwnd != 0 && font != 0 {
		sendMessage.Call(hwnd, 0x0030, font, 1) // WM_SETFONT
	}
}
