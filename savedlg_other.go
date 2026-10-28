//go:build !windows

package main

// saveDialog falls back to the portable explorer, which offers one type: the given format.
func (u *ui) saveDialog(name string, def int) (string, int, bool) {
	f, err := u.expl.CreateFile(name)
	if err != nil {
		return "", 0, false
	}
	path := f.(interface{ Name() string }).Name()
	f.Close()
	return path, def, true
}
