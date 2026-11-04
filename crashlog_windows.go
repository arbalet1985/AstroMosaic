package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func showLogError(err error) {
	message, _ := windows.UTF16PtrFromString("Не удалось включить запись ошибок AstroMosaic:\n" + err.Error())
	title, _ := windows.UTF16PtrFromString("AstroMosaic — ошибка журнала")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}
