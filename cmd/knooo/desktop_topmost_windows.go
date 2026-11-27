package main

import (
	"errors"
	"fmt"
	"unsafe"
)

const wmDesktopTopmost = 0x8004

const (
	topmostQuery uintptr = iota
	topmostOff
	topmostOn
)

var (
	sendMessageTimeout = user32.NewProc("SendMessageTimeoutW")
	setWindowPos       = user32.NewProc("SetWindowPos")
	getWindowLong      = user32.NewProc("GetWindowLongW")
)

func (h *desktopHost) topmost(pinned *bool) (available, current bool, err error) {
	if h == nil {
		return false, false, nil
	}
	hwnd := h.hwnd.Load()
	if hwnd == 0 {
		return false, false, nil
	}
	command := topmostQuery
	if pinned != nil {
		command = topmostOff
		if *pinned {
			command = topmostOn
		}
	}
	const (
		smtoBlock       = 0x0001
		smtoAbortIfHung = 0x0002
		smtoErrorOnExit = 0x0020
	)
	var state uintptr
	ok, _, callErr := sendMessageTimeout.Call(hwnd, wmDesktopTopmost, command, 0,
		smtoBlock|smtoAbortIfHung|smtoErrorOnExit, 1000, uintptr(unsafe.Pointer(&state)))
	if ok == 0 {
		if h.hwnd.Load() == 0 {
			return false, false, nil
		}
		return false, false, fmt.Errorf("读取或修改窗口置顶状态：%w", callErr)
	}
	switch state {
	case topmostOn:
		return true, true, nil
	case topmostOff:
		return true, false, nil
	default:
		return false, false, errors.New("Windows 窗口未能读取或修改置顶状态")
	}
}

func windowTopmost(hwnd, command uintptr) uintptr {
	const (
		hwndTopmost    = ^uintptr(0)
		hwndNotTopmost = ^uintptr(1)
		gwlExStyle     = ^uintptr(19) // -20
		wsExTopmost    = 0x0008
		swpNoSize      = 0x0001
		swpNoMove      = 0x0002
		swpNoActivate  = 0x0010
	)
	switch command {
	case topmostQuery:
	case topmostOff, topmostOn:
		insertAfter := hwndNotTopmost
		if command == topmostOn {
			insertAfter = hwndTopmost
		}
		if ok, _, _ := setWindowPos.Call(hwnd, insertAfter, 0, 0, 0, 0, swpNoSize|swpNoMove|swpNoActivate); ok == 0 {
			return 0
		}
	default:
		return 0
	}
	style, _, _ := getWindowLong.Call(hwnd, gwlExStyle)
	if style&wsExTopmost != 0 {
		return topmostOn
	}
	return topmostOff
}
