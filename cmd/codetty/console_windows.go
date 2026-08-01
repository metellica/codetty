//go:build windows

package main

import "golang.org/x/sys/windows"

// platformInit switches the console to UTF-8 code pages so raw UTF-8 output
// renders correctly under legacy conhost with CJK system code pages
// (932/936/949/950), and returns a restore function for exit.
// Windows Terminal is always UTF-8 and unaffected.
func platformInit() func() {
	inCP, errIn := windows.GetConsoleCP()
	outCP, errOut := windows.GetConsoleOutputCP()
	windows.SetConsoleCP(65001)
	windows.SetConsoleOutputCP(65001)
	return func() {
		if errIn == nil {
			windows.SetConsoleCP(inCP)
		}
		if errOut == nil {
			windows.SetConsoleOutputCP(outCP)
		}
	}
}
