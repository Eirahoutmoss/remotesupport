//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// techDev is one saved device on the technician side (connect by id + password
// without retyping). Stored in %APPDATA%\RemoteSupport\tech-devices.json.
type techDev struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Pass string `json:"pass"`
}

var techDevices []techDev

func techDevPath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	dir := filepath.Join(base, "RemoteSupport")
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "tech-devices.json")
}

func loadTechDevices() {
	p := techDevPath()
	if p == "" {
		return
	}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &techDevices)
	}
}

func saveTechDevices() {
	p := techDevPath()
	if p == "" {
		return
	}
	if b, err := json.Marshal(techDevices); err == nil {
		tmp := p + ".tmp"
		if os.WriteFile(tmp, b, 0600) == nil {
			_ = os.Rename(tmp, p)
		}
	}
}

// upsertTechDevice remembers a device after a successful connect.
func upsertTechDevice(id, pass string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	for i := range techDevices {
		if strings.EqualFold(techDevices[i].ID, id) {
			techDevices[i].Pass = pass
			saveTechDevices()
			return
		}
	}
	techDevices = append(techDevices, techDev{Name: id, ID: id, Pass: pass})
	saveTechDevices()
}

// populateTechDevCombo fills the "Kayıtlı Cihaz" dropdown on the Destek Ver page.
func populateTechDevCombo() {
	if state.techDev == 0 {
		return
	}
	sendMessage.Call(state.techDev, 0x014B, 0, 0) // CB_RESETCONTENT
	addComboItems(state.techDev, "— Kayıtlı cihaz seç —")
	for _, d := range techDevices {
		addComboItems(state.techDev, d.Name)
	}
	sendMessage.Call(state.techDev, 0x014E, 0, 0) // CB_SETCURSEL → placeholder
}

// manageTechDevice offers rename/delete for the saved device selected in the
// "Kayıtlı Cihaz" dropdown, via a small popup menu next to it.
func manageTechDevice() {
	if state.techDev == 0 {
		return
	}
	r, _, _ := sendMessage.Call(state.techDev, 0x0147, 0, 0) // CB_GETCURSEL
	i := int(r) - 1                                          // index 0 is the placeholder
	if i < 0 || i >= len(techDevices) {
		setStatus("● Önce listeden bir kayıtlı cihaz seçin.")
		return
	}
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	const idRename, idDelete = 1, 2
	appendMenuW.Call(menu, 0, uintptr(idRename), uintptr(unsafe.Pointer(utf16ptr("Yeniden Adlandır…"))))
	appendMenuW.Call(menu, 0, uintptr(idDelete), uintptr(unsafe.Pointer(utf16ptr("Sil"))))
	var pt point
	getCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	cmd, _, _ := trackPopupMenu.Call(menu, 0x0100, uintptr(pt.X), uintptr(pt.Y), 0, state.hwnd, 0) // TPM_RETURNCMD
	switch int(cmd) {
	case idRename:
		if name, ok := promptText("Yeniden Adlandır", "Cihaz için yeni ad:", techDevices[i].Name); ok {
			if name = strings.TrimSpace(name); name != "" {
				techDevices[i].Name = name
				saveTechDevices()
				populateTechDevCombo()
				setStatus("● Cihaz yeniden adlandırıldı: " + name)
			}
		}
	case idDelete:
		r2, _, _ := messageBox.Call(0,
			uintptr(unsafe.Pointer(utf16ptr("\""+techDevices[i].Name+"\" kayıtlı cihazı silinsin mi?"))),
			uintptr(unsafe.Pointer(utf16ptr("Kayıtlı Cihazı Sil"))),
			0x00000004|0x00000030) // MB_YESNO | MB_ICONWARNING
		if r2 == 6 { // IDYES
			techDevices = append(techDevices[:i], techDevices[i+1:]...)
			saveTechDevices()
			populateTechDevCombo()
			setText(state.remoteEdit, "")
			setText(state.remotePass, "")
			setStatus("● Kayıtlı cihaz silindi.")
		}
	}
}

// onTechDevSelect fills id + password from the chosen saved device.
func onTechDevSelect() {
	if state.techDev == 0 {
		return
	}
	r, _, _ := sendMessage.Call(state.techDev, 0x0147, 0, 0) // CB_GETCURSEL
	i := int(r) - 1
	if i >= 0 && i < len(techDevices) {
		setText(state.remoteEdit, techDevices[i].ID)
		setText(state.remotePass, techDevices[i].Pass)
		setStatus("● Kayıtlı cihaz seçildi: " + techDevices[i].Name + " — BAĞLAN'a basın.")
	}
}
