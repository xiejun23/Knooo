package main

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/combridge"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

const webViewStartupTimer = 1

var (
	setTimer  = user32.NewProc("SetTimer")
	killTimer = user32.NewProc("KillTimer")
)

type controllerCompleted interface {
	combridge.IUnknown
	controllerCompleted(int32, *webViewObject)
}

type navigationStarting interface {
	combridge.IUnknown
	navigationStarting(*webViewObject) uintptr
}

type newWindowRequested interface {
	combridge.IUnknown
	newWindowRequested(*webViewObject) uintptr
}

func init() {
	combridge.RegisterVTable[combridge.IUnknown, controllerCompleted](
		"{6C4819F3-C9B7-4260-8127-C9F5BDE7F68C}",
		func(this uintptr, status int32, controller *webViewObject) uintptr {
			combridge.Resolve[controllerCompleted](this).controllerCompleted(status, controller)
			return 0
		},
	)
	combridge.RegisterVTable[combridge.IUnknown, navigationStarting](
		"{9ADBE429-F36D-432B-9DDC-F8881FBD76E3}",
		func(this uintptr, _ *webViewObject, args *webViewObject) uintptr {
			return combridge.Resolve[navigationStarting](this).navigationStarting(args)
		},
	)
	combridge.RegisterVTable[combridge.IUnknown, newWindowRequested](
		"{D4C185FE-C81C-4989-97AF-2D3FA7AB5651}",
		func(this uintptr, _ *webViewObject, args *webViewObject) uintptr {
			return combridge.Resolve[newWindowRequested](this).newWindowRequested(args)
		},
	)
}

func (w *desktopWindow) runWebView(data string) error {
	page, err := url.Parse(w.url)
	if err != nil {
		return err
	}
	w.origin = page.Scheme + "://" + page.Host
	profile, err := filepath.Abs(filepath.Join(data, "WebView2"))
	if err != nil {
		return err
	}
	if timer, _, err := setTimer.Call(w.hwnd, webViewStartupTimer, 30_000, 0); timer == 0 {
		return fmt.Errorf("设置 WebView2 启动超时：%w", err)
	}
	defer killTimer.Call(w.hwnd, webViewStartupTimer)
	defer func() {
		if w.controller != nil {
			w.controller.call(controllerClose)
			w.controller.call(comRelease)
			w.controller = nil
		}
	}()

	err = webviewloader.CreateCoreWebView2EnvironmentWithOptions(w, webviewloader.WithUserDataFolder(profile))
	if err != nil {
		return fmt.Errorf("无法启动 Microsoft Edge WebView2。请检查 WebView2 Runtime 是否已安装：%w", err)
	}
	for {
		var message windowMessage
		result, _, err := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		switch int32(result) {
		case -1:
			return fmt.Errorf("读取 Knooo 窗口消息：%w", err)
		case 0:
			return w.webViewError
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&message)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func (w *desktopWindow) failWebView(err error) {
	w.webViewError = err
	postQuitMessage.Call(0)
}

func (w *desktopWindow) EnvironmentCompleted(status webviewloader.HRESULT, object *webviewloader.ICoreWebView2Environment) webviewloader.HRESULT {
	if status < 0 || object == nil {
		w.failWebView(fmt.Errorf("初始化 WebView2 Runtime：HRESULT 0x%08x", uint32(status)))
		return 0
	}
	environment := (*webViewObject)(unsafe.Pointer(object))
	completed := combridge.New[controllerCompleted](w)
	defer completed.Close()
	result := environment.call(environmentCreateController, w.hwnd, completed.Ref())
	if err := webViewResult(result); err != nil {
		w.failWebView(fmt.Errorf("创建 WebView2 窗口：%w", err))
	}
	return 0
}

func (w *desktopWindow) controllerCompleted(status int32, controller *webViewObject) {
	if status < 0 || controller == nil {
		w.failWebView(fmt.Errorf("创建 WebView2 窗口：HRESULT 0x%08x", uint32(status)))
		return
	}
	controller.call(comAddRef)
	w.controller = controller
	w.resize()
	if err := w.configureWebView(); err != nil {
		w.failWebView(fmt.Errorf("打开 Knooo 桌面界面：%w", err))
		return
	}
	killTimer.Call(w.hwnd, webViewStartupTimer)
}

func (w *desktopWindow) configureWebView() error {
	var view *webViewObject
	if result := w.controller.call(controllerGetWebView, uintptr(unsafe.Pointer(&view))); int32(result) < 0 {
		return webViewResult(result)
	}
	defer view.call(comRelease)

	var settings *webViewObject
	if result := view.call(viewGetSettings, uintptr(unsafe.Pointer(&settings))); int32(result) < 0 {
		return webViewResult(result)
	}
	defer settings.call(comRelease)
	for _, setter := range []int{settingsSetWebMessage, settingsSetHostObjects, settingsSetDevTools, settingsSetContextMenu} {
		if err := webViewResult(settings.call(setter, 0)); err != nil {
			return err
		}
	}

	navigation := combridge.New[navigationStarting](w)
	defer navigation.Close()
	popup := combridge.New[newWindowRequested](w)
	defer popup.Close()
	// The controller owns the handlers until Close releases its event subscriptions.
	for _, event := range []struct {
		slot    int
		handler uintptr
	}{
		{viewAddNavigation, navigation.Ref()},
		{viewAddFrameNavigation, navigation.Ref()},
		{viewAddNewWindow, popup.Ref()},
	} {
		var token int64
		result := view.call(event.slot, event.handler, uintptr(unsafe.Pointer(&token)))
		if err := webViewResult(result); err != nil {
			return err
		}
	}

	target, err := windows.UTF16PtrFromString(w.url)
	if err != nil {
		return err
	}
	return webViewResult(view.call(viewNavigate, uintptr(unsafe.Pointer(target))))
}

func (w *desktopWindow) navigationStarting(args *webViewObject) uintptr {
	uri := args.uri()
	user := args.userInitiated(navigationGetUserInitiated)
	if localWebViewURI(w.origin, uri) || user && webViewMailto(uri) {
		return 0
	}
	result := args.call(navigationSetCancel, 1)
	if int32(result) >= 0 && user {
		w.openExternal(uri)
	}
	return result
}

func (w *desktopWindow) newWindowRequested(args *webViewObject) uintptr {
	result := args.call(newWindowSetHandled, 1)
	if int32(result) >= 0 && args.userInitiated(newWindowGetUserInitiated) {
		w.openExternal(args.uri())
	}
	return result
}

func localWebViewURI(origin, uri string) bool {
	address, err := url.Parse(strings.TrimPrefix(uri, "blob:"))
	return err == nil && address.User == nil && address.Scheme+"://"+address.Host == origin
}

// Keep WebView2's normal email handler prompt for the feedback page.
func webViewMailto(uri string) bool {
	address, err := url.Parse(uri)
	return err == nil && address.Scheme == "mailto" && address.Opaque != ""
}

func (w *desktopWindow) openExternal(uri string) {
	address, err := url.Parse(uri)
	if err != nil || address.User != nil || address.Host == "" || (address.Scheme != "https" && address.Scheme != "http") || localWebViewURI(w.origin, uri) {
		return
	}
	go func() {
		if err := openInDefaultBrowser(uri); err != nil {
			log.Print(err)
		}
	}()
}

func openInDefaultBrowser(uri string) error {
	target, err := windows.UTF16PtrFromString(uri)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	if err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
		return fmt.Errorf("初始化 Windows 链接处理程序：%w", err)
	}
	defer windows.CoUninitialize()
	return windows.ShellExecute(0, nil, target, nil, nil, windows.SW_SHOWNORMAL)
}

// WebView2 vtable slots from the Win32 SDK's webview2.idl. Importing the
// generated bindings initializes unused Go callbacks with unsupported parameter sizes.
const (
	comAddRef  = 1
	comRelease = 2

	environmentCreateController = 3

	controllerSetBounds   = 6
	controllerParentMoved = 23
	controllerClose       = 24
	controllerGetWebView  = 25

	viewGetSettings        = 3
	viewNavigate           = 5
	viewAddNavigation      = 7
	viewAddFrameNavigation = 17
	viewAddNewWindow       = 44

	settingsSetWebMessage  = 6
	settingsSetDevTools    = 12
	settingsSetContextMenu = 14
	settingsSetHostObjects = 16

	eventGetURI                = 3
	navigationGetUserInitiated = 4
	navigationSetCancel        = 8
	newWindowSetHandled        = 6
	newWindowGetUserInitiated  = 8
)

type webViewObject struct {
	vtable *[viewAddNewWindow + 1]uintptr
}

//go:uintptrescapes
func (o *webViewObject) call(slot int, args ...uintptr) uintptr {
	result, _, _ := syscall.SyscallN(o.vtable[slot], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return result
}

func (o *webViewObject) uri() string {
	var uri *uint16
	if result := o.call(eventGetURI, uintptr(unsafe.Pointer(&uri))); int32(result) < 0 || uri == nil {
		return ""
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(uri))
	return windows.UTF16PtrToString(uri)
}

func (o *webViewObject) userInitiated(slot int) bool {
	var user int32
	result := o.call(slot, uintptr(unsafe.Pointer(&user)))
	return int32(result) >= 0 && user != 0
}

func webViewResult(result uintptr) error {
	if int32(result) < 0 {
		return syscall.Errno(uint32(result))
	}
	return nil
}
