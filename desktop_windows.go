package main

import (
	"fmt"
	"runtime"
	"syscall"

	"golang.org/x/sys/windows"
)

func openDesktop(url string) error {
	target, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}

	// Some URL handlers need a COM apartment on the calling thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	if err != nil && err != syscall.Errno(windows.S_FALSE) {
		return fmt.Errorf("initialize Windows shell: %w", err)
	}
	defer windows.CoUninitialize()

	if err := windows.ShellExecute(0, nil, target, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("open default browser: %w", err)
	}
	return nil
}
