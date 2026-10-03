//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/eirahoutmoss/remotesupport/client/capture"
	clientsignaling "github.com/eirahoutmoss/remotesupport/client/signaling"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"golang.org/x/sys/windows"
)

type point struct{ X, Y int32 }
type openFileName struct {
	StructSize    uint32
	Owner         uintptr
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	Hook          uintptr
	TemplateName  *uint16
	Reserved      uintptr
	Reserved2     uint32
	FlagsEx       uint32
}

type fileReceiveState struct {
	mu       sync.Mutex
	file     *os.File
	name     string
	total    int64
	received int64
	tempPath string
}

var fileRx fileReceiveState
var fileSendMu sync.Mutex

type rect struct{ Left, Top, Right, Bottom int32 }
type wndclassex struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}
type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}
type windowState struct {
	hwnd               uintptr
	mode               int
	status             uintptr
	codeLabel          uintptr
	codeEdit           uintptr
	remoteEdit         uintptr
	monitorButtons     []uintptr
	action             uintptr
	copy               uintptr
	back               uintptr
	homeGet            uintptr
	homeGive           uintptr
	fileSend           uintptr
	resolutionButtons  []uintptr
	fullscreenButton   uintptr
	elevate            uintptr
	clipSend           uintptr
	clipGet            uintptr
	chatInput          uintptr
	chatSend           uintptr
	settingsEndpoint   uintptr
	settingsQuality    uintptr
	settingsResolution uintptr
	settingsSave       uintptr
	settingsReset      uintptr
	settingsIdle       uintptr
	settingsIdleAction uintptr
	settingsInternet   uintptr
	remotePass         uintptr
	devOn              uintptr
	devId              uintptr
	devPass            uintptr
	devSave            uintptr
	techDev            uintptr
	techDevManage      uintptr
	aboutTheme         uintptr
	aboutUpdate        uintptr
	settingsTurnURL    uintptr
	settingsTurnUser   uintptr
	settingsTurnPass   uintptr
	stayOpen           uintptr
	actionsBtn         uintptr
	shareLink          uintptr
	header             uintptr
	subtitle           uintptr
	statusText         string

	mu     sync.Mutex
	sig    *clientsignaling.Client
	peer   *webrtcpeer.Peer
	cancel context.CancelFunc
	ctx    context.Context
}

var state windowState

var viewer viewerState

var (
	uiBgBrush      uintptr
	uiPanelBrush   uintptr
	uiEditBrush    uintptr
	uiHeaderFont   uintptr
	uiBrandFont    uintptr
	uiTitleFont    uintptr
	uiSubtitleFont uintptr
	uiBodyFont     uintptr
	uiButtonFont   uintptr
	uiLabelFont    uintptr
	uiSmallFont    uintptr
	uiCodeFont     uintptr
	uiEditFont     uintptr
	uiIconFont     uintptr
)

// Palette. Values are written as intuitive 0xRRGGBB and converted to the
// Win32 COLORREF byte order (0x00BBGGRR) by rgb() at every use site. Writing
// them as RGB and passing them straight to GDI (as the first version did) makes
// GDI read them as BGR, which turned the blue accent orange and the navy canvas
// brown; rgb() is what actually gives the concept its blue/navy look.
// Active palette. These are runtime variables (not constants) so the user can
// switch themes from the Hakkında page; setThemeVars/applyThemePreset in
// ui_themes.go overwrite them. Initial values are the "Sıcak" preset; the saved
// preset is applied at startup before the first paint.
var (
	uiBgColor     uint32 = 0x14100A // app canvas (top of gradient)
	uiBgColor2    uint32 = 0x1B150D // app canvas, bottom of gradient
	uiBarColor    uint32 = 0x18120B // top product bar + footer
	uiNavColor    uint32 = 0x100C07 // left navigation rail (darkest surface)
	uiPanelColor  uint32 = 0x241B12 // cards / panels
	uiPanelHi     uint32 = 0x312517 // hovered / active panel
	uiEditColor   uint32 = 0x0E0A06 // input fields, code box
	uiBorderColor uint32 = 0x3D2F1E // hairline dividers / borders
	uiTextColor   uint32 = 0xF8F1E6 // primary text
	uiMutedColor  uint32 = 0xC2AC8E // secondary text
	uiFaintColor  uint32 = 0x8A7458 // tertiary / placeholder text
	uiAccentColor uint32 = 0xF5A623 // primary accent
	uiAccentHi    uint32 = 0xFFBE4D // accent highlight (gradient top / glow)
	uiAccentDim   uint32 = 0xB97608 // accent shadow (gradient bottom)
	uiCyanColor   uint32 = 0xFF8A5B // secondary accent on icons
	uiSuccess     uint32 = 0x7FB83E // "connected" / live status
	uiDanger      uint32 = 0xEF5533 // disconnect / destructive
	uiDangerHi    uint32 = 0xF56B4C // disconnect highlight
	uiWarn        uint32 = 0xFFC247 // caution (medium link quality)
)

type appSettings struct {
	SignalingURL string `json:"signaling_url"`
	Quality      int    `json:"quality"`
	MaxHeight    int    `json:"max_height"`
	InputEnabled bool   `json:"input_enabled"`
	IdleWarnMin  int    `json:"idle_warn_min"`
	IdleClose    bool   `json:"idle_close"`
	TurnURL      string `json:"turn_url"`
	TurnUser     string `json:"turn_user"`
	TurnPass     string `json:"turn_pass"`
	// InternetCode opts in to the serverless "internet code": UPnP opens
	// TCP 8091 on the home router and the code embeds this PC's public IP.
	// Off by default — with a central signaling server it only adds exposure.
	InternetCode bool `json:"internet_code"`

	// Device access: this machine registers with the server by a stable id +
	// password and waits for a technician to connect by that id + password.
	DeviceAccessOn bool   `json:"device_access_on"`
	DeviceID       string `json:"device_id"`
	DevicePass     string `json:"device_pass"`

	// ThemePreset: "gece" (Gece Mavisi), "sicak" (Sıcak), "acik" (Aydınlık).
	// Empty falls back to the default in setThemeVars.
	ThemePreset string `json:"theme_preset"`
}

var settingsMu sync.RWMutex
var appCfg = appSettings{Quality: 88, MaxHeight: 0, InputEnabled: true, IdleWarnMin: 10, IdleClose: false}

func settingsPath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	dir := filepath.Join(base, "RemoteSupport")
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "settings.json")
}

func loadSettings() {
	path := settingsPath()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cfg appSettings
	if json.Unmarshal(b, &cfg) != nil {
		return
	}
	if cfg.Quality != 76 && cfg.Quality != 88 {
		cfg.Quality = 88
	}
	if cfg.MaxHeight != 0 && cfg.MaxHeight != 1080 && cfg.MaxHeight != 720 && cfg.MaxHeight != 480 {
		cfg.MaxHeight = 0
	}
	if cfg.IdleWarnMin != 0 && cfg.IdleWarnMin != 10 && cfg.IdleWarnMin != 20 && cfg.IdleWarnMin != 30 {
		cfg.IdleWarnMin = 10
	}
	settingsMu.Lock()
	appCfg = cfg
	settingsMu.Unlock()
}

func saveSettings() {
	path := settingsPath()
	if path == "" {
		return
	}
	settingsMu.RLock()
	cfg := appCfg
	settingsMu.RUnlock()
	b, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(path, b, 0600)
}

func currentSettings() appSettings { settingsMu.RLock(); defer settingsMu.RUnlock(); return appCfg }

var targetMonitorCache struct {
	sync.Once
	monitor capture.Monitor
	ok      bool
}

var sameHostPeer atomic.Bool
var embeddedSignalingReady atomic.Bool
var sidebarPage atomic.Int32
var activeMonitor atomic.Int32

// Live session telemetry, all read from the paint thread and written from
// worker goroutines, so every field is atomic or mutex-guarded.
var sessionStartUnix atomic.Int64 // connect time (unix nanos); 0 = not connected
var framesRecv atomic.Uint64      // total frames the operator has rendered
var fpsValue atomic.Int32         // frames/second, refreshed once per second
var capturing atomic.Bool         // agent is actively streaming its own screen
var lastActivityUnix atomic.Int64 // last remote-control input (either side)
var idleWarn atomic.Bool          // idle warning currently shown
var curtainHwnd uintptr           // agent-side privacy curtain window
var remoteCurtainOn atomic.Bool   // operator: curtain toggle state
var remoteLockOn atomic.Bool      // operator: input-lock toggle state
var remoteHostMu sync.Mutex
var remoteHostName string

func setRemoteHost(h string) { remoteHostMu.Lock(); remoteHostName = h; remoteHostMu.Unlock() }
func getRemoteHost() string  { remoteHostMu.Lock(); defer remoteHostMu.Unlock(); return remoteHostName }

var connInfoMu sync.Mutex
var connPathStr string // "direct" / "relay" / ""
var sasStr string      // 6-digit SAS once known
var activeCodeStr string

func setActiveCode(code string) {
	connInfoMu.Lock()
	activeCodeStr = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	connInfoMu.Unlock()
}

func getActiveCode() string {
	connInfoMu.Lock()
	defer connInfoMu.Unlock()
	return activeCodeStr
}

func setConnInfo(path, sas string) {
	connInfoMu.Lock()
	if path != "" {
		connPathStr = path
	}
	if sas != "" {
		sasStr = sas
	}
	connInfoMu.Unlock()
}

func getConnInfo() (string, string) {
	connInfoMu.Lock()
	defer connInfoMu.Unlock()
	return connPathStr, sasStr
}

func resetConnInfo() {
	connInfoMu.Lock()
	connPathStr, sasStr = "", ""
	connInfoMu.Unlock()
	sessionStartUnix.Store(0)
	framesRecv.Store(0)
	fpsValue.Store(0)
	capturing.Store(false)
	idleWarn.Store(false)
}

func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// restartAsAdmin relaunches this executable through the standard UAC prompt
// (ShellExecute "runas"). It never bypasses UAC; the user still consents.
func restartAsAdmin() {
	exe, err := os.Executable()
	if err != nil {
		setStatus("● Yönetici olarak başlatılamadı: " + err.Error())
		return
	}
	verb := utf16ptr("runas")
	file := utf16ptr(exe)
	r, _, _ := shellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1) // SW_SHOWNORMAL
	if r <= 32 {
		// User declined the prompt or it failed; stay running unelevated.
		setStatus("● Yükseltme iptal edildi.")
		return
	}
	postQuit.Call(0) // elevated instance is starting; close this one
}

// sessionElapsed formats the live session duration as MM:SS.
func sessionElapsed() string {
	start := sessionStartUnix.Load()
	if start == 0 {
		return "00:00"
	}
	d := time.Since(time.Unix(0, start))
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

// startSessionStats refreshes FPS, path and SAS once per second while the
// session is live, then repaints the shell so the stat strip stays current.
func startSessionStats(peer *webrtcpeer.Peer) {
	sessionStartUnix.Store(time.Now().UnixNano())
	lastActivityUnix.Store(time.Now().Unix())
	framesRecv.Store(0)
	go func() {
		defer logCrash("sessionStats")
		var last uint64
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if sessionStartUnix.Load() == 0 || peerDead(peer) {
				return
			}
			now := framesRecv.Load()
			fpsValue.Store(int32(now - last))
			last = now
			if path := peer.PathKind(); path != "" {
				relayPath.Store(path == "relay")
				setConnInfo(path, "")
			}
			if _, sas := getConnInfo(); sas == "" {
				if v, ok := peer.FingerprintSAS(getActiveCode()); ok {
					setConnInfo("", v)
				}
			}
			cfg := currentSettings()
			if cfg.IdleWarnMin > 0 {
				idle := time.Now().Unix() - lastActivityUnix.Load()
				warnAt := int64(cfg.IdleWarnMin) * 60
				if idle >= warnAt {
					if !idleWarn.Swap(true) && state.hwnd != 0 {
						postMessage.Call(state.hwnd, WM_APP_IDLE, 0, 0)
					}
					if cfg.IdleClose && idle >= warnAt+60 && state.hwnd != 0 {
						postMessage.Call(state.hwnd, WM_APP_IDLE_CLOSE, 0, 0)
						return
					}
				} else if idleWarn.Swap(false) && state.hwnd != 0 {
					postMessage.Call(state.hwnd, WM_APP_IDLE, 0, 0)
				}
			}
			if state.hwnd != 0 {
				invalidateRect.Call(state.hwnd, 0, 0)
			}
		}
	}()
}

var remoteMonitorsMu sync.RWMutex
var remoteMonitors []capture.Monitor

var activeTargetMonitorMu sync.RWMutex
var activeTargetMonitor capture.Monitor
var activeTargetMonitorOK bool

var captureMaxHeight atomic.Int32
var fullscreen atomic.Bool
var fullscreenSavedStyle uintptr
var fullscreenSavedRect rect
var fullscreenSavedValid bool

type viewerState struct {
	hwnd   uintptr
	active bool
	mu     sync.RWMutex
	pixels []byte
	width  int
	height int
	seq    uint64

	// Fit is the default remote-desktop view. When zoomed, panX/panY are
	// the image's top-left corner in viewer-client coordinates.
	fit         bool
	zoom        float64
	panX        float64
	panY        float64
	dragging    bool
	clearNeeded bool
	dragX       int
	dragY       int
	lastMoveAt  time.Time
}

type winMouseInput struct {
	Dx, Dy      int32
	MouseData   uint32
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type winKeyboardInput struct {
	Vk          uint16
	Scan        uint16
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type winInput struct {
	Type uint32
	Pad  uint32
	Mi   winMouseInput
}

type winKeyInput struct {
	Type uint32
	Pad  uint32
	Ki   winKeyboardInput
	Pad2 [8]byte // INPUT union is 32 bytes on Windows x64
}

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove       = 0x0001
	mouseLeftDown   = 0x0002
	mouseLeftUp     = 0x0004
	mouseRightDown  = 0x0008
	mouseRightUp    = 0x0010
	mouseMiddleDown = 0x0020
	mouseMiddleUp   = 0x0040
	mouseWheel      = 0x0800
	mouseAbsolute   = 0x8000
	mouseVirtual    = 0x4000

	keyUp       = 0x0002
	keyScancode = 0x0008
	keyExtended = 0x0001
)

type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}
type bitmapInfo struct {
	BmiHeader bitmapInfoHeader
	BmiColors [3]uint32
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

type paintStruct struct {
	hdc         uintptr
	erase       int32
	rcPaint     rect
	restore     int32
	incUpdate   int32
	rgbReserved [32]byte
}
