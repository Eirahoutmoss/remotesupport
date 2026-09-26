//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procMessageBoxW = user32.NewProc("MessageBoxW")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
)

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func main() {
	// First visible Windows milestone.
	// Keep this deliberately dependency-free: double-clicking the EXE
	// must immediately produce a native Windows UI.
	const title = "Remote Support"
	const body = "Remote Support\n\nHazır\n\nBağlantı kodu: 482 731\n\nEkran paylaşımı altyapısı hazır."
	procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr(body))),
		uintptr(unsafe.Pointer(utf16Ptr(title))),
		0x40, // MB_ICONINFORMATION
	)
	_, _ = procGetConsoleWindow.Call()
	fmt.Print("")
}
