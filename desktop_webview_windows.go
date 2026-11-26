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
	"github.com/wailsapp/go-webview2/pkg/webview2"
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
	controllerCompleted(int32, *webview2.ICoreWebView2Controller)
}

type navigationStarting interface {
	combridge.IUnknown
	navigationStarting(*webview2.ICoreWebView2NavigationStartingEventArgs) uintptr
}

type newWindowRequested interface {
	combridge.IUnknown
	newWindowRequested(*webview2.ICoreWebView2NewWindowRequestedEventArgs) uintptr
}

func init() {
	combridge.RegisterVTable[combridge.IUnknown, controllerCompleted](
		"{6C4819F3-C9B7-4260-8127-C9F5BDE7F68C}",
		func(this uintptr, status int32, controller *webview2.ICoreWebView2Controller) uintptr {
			combridge.Resolve[controllerCompleted](this).controllerCompleted(status, controller)
			return 0
		},
	)
	combridge.RegisterVTable[combridge.IUnknown, navigationStarting](
		"{9ADBE429-F36D-432B-9DDC-F8881FBD76E3}",
		func(this uintptr, _ *webview2.ICoreWebView2, args *webview2.ICoreWebView2NavigationStartingEventArgs) uintptr {
			return combridge.Resolve[navigationStarting](this).navigationStarting(args)
		},
	)
	combridge.RegisterVTable[combridge.IUnknown, newWindowRequested](
		"{D4C185FE-C81C-4989-97AF-2D3FA7AB5651}",
		func(this uintptr, _ *webview2.ICoreWebView2, args *webview2.ICoreWebView2NewWindowRequestedEventArgs) uintptr {
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
			_ = w.controller.Close()
			w.controller.Vtbl.CallRelease(unsafe.Pointer(w.controller))
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
	environment := (*webview2.ICoreWebView2Environment)(unsafe.Pointer(object))
	completed := combridge.New[controllerCompleted](w)
	defer completed.Close()
	// The generated binding passes the HWND by address, but this COM method takes it by value.
	result, _, _ := environment.Vtbl.CreateCoreWebView2Controller.Call(uintptr(unsafe.Pointer(environment)), w.hwnd, completed.Ref())
	if err := webViewResult(result); err != nil {
		w.failWebView(fmt.Errorf("创建 WebView2 窗口：%w", err))
	}
	return 0
}

func (w *desktopWindow) controllerCompleted(status int32, controller *webview2.ICoreWebView2Controller) {
	if status < 0 || controller == nil {
		w.failWebView(fmt.Errorf("创建 WebView2 窗口：HRESULT 0x%08x", uint32(status)))
		return
	}
	controller.AddRef()
	w.controller = controller
	w.resize()
	if err := w.configureWebView(); err != nil {
		w.failWebView(fmt.Errorf("打开 Knooo 桌面界面：%w", err))
		return
	}
	killTimer.Call(w.hwnd, webViewStartupTimer)
}

func (w *desktopWindow) configureWebView() error {
	view, err := w.controller.GetCoreWebView2()
	if err != nil {
		return err
	}
	defer view.Vtbl.CallRelease(unsafe.Pointer(view))
	settings, err := view.GetSettings()
	if err != nil {
		return err
	}
	defer settings.Vtbl.CallRelease(unsafe.Pointer(settings))
	// The COM ABI takes BOOL by value. Some generated setters pass a pointer.
	for _, setter := range []webview2.ComProc{
		settings.Vtbl.PutIsWebMessageEnabled,
		settings.Vtbl.PutAreHostObjectsAllowed,
		settings.Vtbl.PutAreDevToolsEnabled,
		settings.Vtbl.PutAreDefaultContextMenusEnabled,
	} {
		result, _, _ := setter.Call(uintptr(unsafe.Pointer(settings)), 0)
		if err := webViewResult(result); err != nil {
			return err
		}
	}

	navigation := combridge.New[navigationStarting](w)
	defer navigation.Close()
	popup := combridge.New[newWindowRequested](w)
	defer popup.Close()
	// The controller owns the handlers until Close releases its event subscriptions.
	for _, event := range []struct {
		register webview2.ComProc
		handler  uintptr
	}{
		{view.Vtbl.AddNavigationStarting, navigation.Ref()},
		{view.Vtbl.AddFrameNavigationStarting, navigation.Ref()},
		{view.Vtbl.AddNewWindowRequested, popup.Ref()},
	} {
		var token webview2.EventRegistrationToken
		result, _, _ := event.register.Call(uintptr(unsafe.Pointer(view)), event.handler, uintptr(unsafe.Pointer(&token)))
		if err := webViewResult(result); err != nil {
			return err
		}
	}

	target, err := windows.UTF16PtrFromString(w.url)
	if err != nil {
		return err
	}
	// Navigate takes LPCWSTR, not an address of a pointer.
	result, _, _ := view.Vtbl.Navigate.Call(uintptr(unsafe.Pointer(view)), uintptr(unsafe.Pointer(target)))
	return webViewResult(result)
}

func (w *desktopWindow) navigationStarting(args *webview2.ICoreWebView2NavigationStartingEventArgs) uintptr {
	uri := webViewURI(args.Vtbl.GetUri, unsafe.Pointer(args))
	user, _ := args.GetIsUserInitiated()
	if localWebViewURI(w.origin, uri) || user && webViewMailto(uri) {
		return 0
	}
	result, _, _ := args.Vtbl.PutCancel.Call(uintptr(unsafe.Pointer(args)), 1)
	if int32(result) >= 0 && user {
		w.openExternal(uri)
	}
	return result
}

func (w *desktopWindow) newWindowRequested(args *webview2.ICoreWebView2NewWindowRequestedEventArgs) uintptr {
	result, _, _ := args.Vtbl.PutHandled.Call(uintptr(unsafe.Pointer(args)), 1)
	if int32(result) >= 0 {
		if user, _ := args.GetIsUserInitiated(); user {
			w.openExternal(webViewURI(args.Vtbl.GetUri, unsafe.Pointer(args)))
		}
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

func webViewURI(getURI webview2.ComProc, object unsafe.Pointer) string {
	var uri *uint16
	result, _, _ := getURI.Call(uintptr(object), uintptr(unsafe.Pointer(&uri)))
	if int32(result) < 0 || uri == nil {
		return ""
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(uri))
	return windows.UTF16PtrToString(uri)
}

func webViewResult(result uintptr) error {
	if int32(result) < 0 {
		return syscall.Errno(uint32(result))
	}
	return nil
}
