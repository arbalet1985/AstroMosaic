package main

import (
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getSaveFileName = windows.NewLazySystemDLL("comdlg32.dll").NewProc("GetSaveFileNameW")

// openFileName is OPENFILENAMEW.
type openFileName struct {
	structSize      uint32
	owner           uintptr
	instance        uintptr
	filter          *uint16
	customFilter    *uint16
	maxCustomFilter uint32
	filterIndex     uint32
	file            *uint16
	maxFile         uint32
	fileTitle       *uint16
	maxFileTitle    uint32
	initialDir      *uint16
	title           *uint16
	flags           uint32
	fileOffset      uint16
	fileExtension   uint16
	defExt          *uint16
	custData        uintptr
	fnHook          uintptr
	templateName    *uint16
	pvReserved      uintptr
	dwReserved      uint32
	flagsEx         uint32
}

// saveDialog asks for a file name and a format: the "Save as type" list holds saveFormats and
// starts at def. ok is false when the user cancels.
func (u *ui) saveDialog(name string, def int) (path string, format int, ok bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var f []uint16
	for _, sf := range saveFormats {
		pat := "*." + sf.ext
		if sf.ext == "fits" {
			pat = "*.fits;*.fit;*.fts"
		}
		f = append(f, windows.StringToUTF16(sf.label+" ("+pat+")")...)
		f = append(f, windows.StringToUTF16(pat)...)
	}
	f = append(f, 0)
	buf := make([]uint16, 32768)
	copy(buf, windows.StringToUTF16(name))
	o := openFileName{
		filter:      &f[0],
		filterIndex: uint32(def + 1),
		file:        &buf[0],
		maxFile:     uint32(len(buf)),
		defExt:      &windows.StringToUTF16(saveFormats[def].ext)[0], // the dialog swaps in the chosen type's extension
		flags:       0x2 | 0x8 | 0x800 | 0x80000,                     // OVERWRITEPROMPT | NOCHANGEDIR | PATHMUSTEXIST | EXPLORER
	}
	o.structSize = uint32(unsafe.Sizeof(o))
	if r, _, _ := getSaveFileName.Call(uintptr(unsafe.Pointer(&o))); r == 0 {
		return "", 0, false
	}
	path = windows.UTF16ToString(buf)
	format = min(max(int(o.filterIndex)-1, 0), len(saveFormats)-1)
	return path, format, strings.TrimSpace(path) != ""
}
