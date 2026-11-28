package main

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
void knoooRun(const char *url);
void knoooStop(void);
void knoooError(const char *message);
*/
import "C"

import (
	"errors"
	"runtime"
	"unsafe"
)

func init() { runtime.LockOSThread() }

type desktopHost struct{}

func newDesktopHost(_, tray bool) (*desktopHost, error) {
	if tray {
		return nil, errors.New("--tray 仅支持 Windows")
	}
	return &desktopHost{}, nil
}

func (*desktopHost) close()                            {}
func (*desktopHost) topmost(*bool) (bool, bool, error) { return false, false, nil }
func attachHeadlessConsole()                           {}

func showDesktopError(err error) {
	message := C.CString(err.Error())
	defer C.free(unsafe.Pointer(message))
	C.knoooError(message)
}

func (*desktopHost) run(url, _ string, stop <-chan struct{}) error {
	address := C.CString(url)
	defer C.free(unsafe.Pointer(address))
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			C.knoooStop()
		case <-done:
		}
	}()
	C.knoooRun(address)
	return nil
}
