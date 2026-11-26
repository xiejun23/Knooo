package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmDestroy         = 0x0002
	wmMove            = 0x0003
	wmSize            = 0x0005
	wmClose           = 0x0010
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
	wmContextMenu     = 0x007b
	wmTimer           = 0x0113
	wmLeftDoubleClick = 0x0203
	wmRightButtonUp   = 0x0205
	wmTray            = 0x8001
	wmShowDesktop     = 0x8002
	wmExitDesktop     = 0x8003

	notifySelect    = 0x0400
	notifyKeySelect = 0x0401
	notifyAdd       = 0
	notifyDelete    = 2
	notifyVersion   = 4

	trayOpen = 1
	trayQuit = 2
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	shell32          = windows.NewLazySystemDLL("shell32.dll")
	registerClass    = user32.NewProc("RegisterClassW")
	createWindow     = user32.NewProc("CreateWindowExW")
	destroyWindow    = user32.NewProc("DestroyWindow")
	findWindow       = user32.NewProc("FindWindowW")
	messageBox       = user32.NewProc("MessageBoxW")
	loadIcon         = user32.NewProc("LoadIconW")
	loadCursor       = user32.NewProc("LoadCursorW")
	defWindowProc    = user32.NewProc("DefWindowProcW")
	getMessage       = user32.NewProc("GetMessageW")
	translateMessage = user32.NewProc("TranslateMessage")
	dispatchMessage  = user32.NewProc("DispatchMessageW")
	postMessage      = user32.NewProc("PostMessageW")
	postQuitMessage  = user32.NewProc("PostQuitMessage")
	registerMessage  = user32.NewProc("RegisterWindowMessageW")
	showWindow       = user32.NewProc("ShowWindow")
	setForeground    = user32.NewProc("SetForegroundWindow")
	allowForeground  = user32.NewProc("AllowSetForegroundWindow")
	isIconic         = user32.NewProc("IsIconic")
	getClientRect    = user32.NewProc("GetClientRect")
	getCursorPos     = user32.NewProc("GetCursorPos")
	createPopupMenu  = user32.NewProc("CreatePopupMenu")
	appendMenu       = user32.NewProc("AppendMenuW")
	setMenuDefault   = user32.NewProc("SetMenuDefaultItem")
	trackPopupMenu   = user32.NewProc("TrackPopupMenu")
	destroyMenu      = user32.NewProc("DestroyMenu")
	systemParameters = user32.NewProc("SystemParametersInfoW")
	shellNotifyIcon  = shell32.NewProc("Shell_NotifyIconW")
)

type desktopHost struct {
	class       string
	mutex       windows.Handle
	startHidden bool
}

func newDesktopHost(single, tray bool) (*desktopHost, error) {
	host := &desktopHost{class: fmt.Sprintf("Knooo.Studio.Window.%d", os.Getpid()), startHidden: tray}
	if !single {
		return host, nil
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("读取 Windows 登录用户：%w", err)
	}
	sid := user.User.Sid
	host.class = "Knooo.Studio.Window." + sid.String()
	name := windows.StringToUTF16Ptr(`Local\Knooo.Studio.` + sid.String())
	class := windows.StringToUTF16Ptr(host.class)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mutex, err := windows.CreateMutex(nil, false, name)
		if err == nil {
			host.mutex = mutex
			return host, nil
		}
		if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return nil, fmt.Errorf("检查 Knooo 当前实例：%w", err)
		}
		_ = windows.CloseHandle(mutex)
		if tray {
			return nil, nil
		}
		hwnd, _, _ := findWindow.Call(uintptr(unsafe.Pointer(class)), 0)
		var pid uint32
		if hwnd != 0 {
			_, _ = windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid)
		}
		if pid != 0 && isWindowOwner(pid, sid) {
			allowForeground.Call(uintptr(pid))
			if ok, _, _ := postMessage.Call(hwnd, wmShowDesktop, 0, 0); ok != 0 {
				return nil, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, errors.New("Knooo 已在运行，但无法打开窗口。请从系统托盘打开 Knooo。")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func isWindowOwner(pid uint32, sid *windows.SID) bool {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	return err == nil && sid.Equals(user.User.Sid)
}

func (h *desktopHost) close() {
	if h.mutex != 0 {
		_ = windows.CloseHandle(h.mutex)
	}
}

func attachHeadlessConsole() {
	_, stdoutErr := windows.GetFileType(windows.Handle(os.Stdout.Fd()))
	_, stderrErr := windows.GetFileType(windows.Handle(os.Stderr.Fd()))
	if stdoutErr == nil && stderrErr == nil {
		return
	}
	// GUI builds have no console by default, including when started from a terminal.
	attach := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	const attachParentConsole = 0xffffffff
	attach.Call(attachParentConsole)
	if stdoutErr != nil {
		if handle, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil && handle != 0 && handle != windows.InvalidHandle {
			os.Stdout = os.NewFile(uintptr(handle), "stdout")
		}
	}
	if stderrErr != nil {
		if handle, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE); err == nil && handle != 0 && handle != windows.InvalidHandle {
			os.Stderr = os.NewFile(uintptr(handle), "stderr")
			log.SetOutput(os.Stderr)
		}
	}
}

func showDesktopError(err error) {
	message := windows.StringToUTF16Ptr(err.Error())
	title := windows.StringToUTF16Ptr("Knooo")
	messageBox.Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}

type windowClass struct {
	style       uint32
	proc        uintptr
	classExtra  int32
	windowExtra int32
	instance    windows.Handle
	icon        uintptr
	cursor      uintptr
	background  uintptr
	menuName    *uint16
	className   *uint16
}

type windowPoint struct {
	x, y int32
}

type windowMessage struct {
	window         uintptr
	message        uint32
	wParam, lParam uintptr
	time           uint32
	point          windowPoint
	private        uint32
}

type trayIconData struct {
	size             uint32
	window           uintptr
	id               uint32
	flags            uint32
	message          uint32
	icon             uintptr
	tip              [128]uint16
	state, stateMask uint32
	info             [256]uint16
	version          uint32
	infoTitle        [64]uint16
	infoFlags        uint32
	guid             windows.GUID
	balloonIcon      uintptr
}

type desktopWindow struct {
	hwnd           uintptr
	taskbarCreated uint32
	tray           trayIconData
	trayPresent    bool
	visible        bool
	controller     *webViewObject
	memoryView     *webViewObject
	webViewStarted bool
	webViewError   error
	origin         string
	data           string
	url            string
}

func (h *desktopHost) run(url, data string, stop <-chan struct{}) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && err != syscall.Errno(windows.S_FALSE) {
		return fmt.Errorf("初始化 Knooo 桌面窗口：%w", err)
	}
	defer windows.CoUninitialize()
	if data == "" {
		var err error
		data, err = os.MkdirTemp("", "knooo-webview2-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(data)
	}

	var module windows.Handle
	if err := windows.GetModuleHandleEx(windows.GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT, nil, &module); err != nil {
		return err
	}
	title := windows.StringToUTF16Ptr("Knooo")
	icon, _, _ := loadIcon.Call(uintptr(module), 1)
	if icon == 0 {
		icon, _, _ = loadIcon.Call(0, 32512)
	}
	cursor, _, _ := loadCursor.Call(0, 32512)
	taskbarMessage := windows.StringToUTF16Ptr("TaskbarCreated")
	taskbarCreated, _, _ := registerMessage.Call(uintptr(unsafe.Pointer(taskbarMessage)))
	window := &desktopWindow{taskbarCreated: uint32(taskbarCreated), data: data, url: url}
	class := windowClass{
		proc:     windows.NewCallback(window.message),
		instance: module, icon: icon, cursor: cursor, background: 6,
		className: windows.StringToUTF16Ptr(h.class),
	}
	if atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return fmt.Errorf("注册 Knooo 窗口：%w", err)
	}
	var workArea windows.Rect
	systemParameters.Call(0x0030, 0, uintptr(unsafe.Pointer(&workArea)), 0)
	width, height := int32(1240), int32(800)
	if workArea.Right > workArea.Left {
		width = min(width, workArea.Right-workArea.Left)
		height = min(height, workArea.Bottom-workArea.Top)
	}
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(class.className)), uintptr(unsafe.Pointer(title)),
		0x00cf0000, 0x80000000, 0x80000000, uintptr(width), uintptr(height), 0, 0, uintptr(module), 0)
	if hwnd == 0 {
		return fmt.Errorf("创建 Knooo 窗口：%w", err)
	}
	window.hwnd = hwnd
	defer destroyWindow.Call(hwnd)

	window.tray = trayIconData{id: 1, window: hwnd, flags: 0x7, message: wmTray, icon: icon, version: 4}
	window.tray.size = uint32(unsafe.Sizeof(window.tray))
	copy(window.tray.tip[:], windows.StringToUTF16("Knooo"))
	window.addTrayIcon()
	defer shellNotifyIcon.Call(notifyDelete, uintptr(unsafe.Pointer(&window.tray)))
	if !h.startHidden || !window.trayPresent {
		window.show()
	}

	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-closed:
		case <-stop:
			postMessage.Call(hwnd, wmExitDesktop, 0, 0)
		}
	}()
	return window.runMessageLoop()
}

func (w *desktopWindow) message(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmSize:
		w.resize()
	case wmMove:
		if w.controller != nil {
			w.controller.call(controllerParentMoved)
		}
	case wmQueryEndSession:
		return 1
	case wmEndSession:
		if wParam != 0 {
			postQuitMessage.Call(0)
		}
		return 0
	case wmClose:
		if w.trayPresent {
			w.visible = false
			showWindow.Call(hwnd, windows.SW_HIDE)
			w.setWebViewMemoryTarget()
		} else {
			postQuitMessage.Call(0)
		}
		return 0
	case wmTimer:
		if wParam == webViewStartupTimer && w.webViewStarted && w.controller == nil {
			w.failWebView(errors.New("启动 WebView2 超时。请检查 Microsoft Edge WebView2 Runtime。"))
		}
		return 0
	case wmDestroy, wmExitDesktop:
		postQuitMessage.Call(0)
		return 0
	case wmShowDesktop:
		w.show()
		return 0
	case wmTray:
		switch uint16(lParam) {
		case notifySelect, notifyKeySelect, wmLeftDoubleClick:
			w.show()
		case wmContextMenu, wmRightButtonUp:
			w.showTrayMenu()
		}
		return 0
	default:
		if message != 0 && message == w.taskbarCreated && w.hwnd != 0 {
			w.addTrayIcon()
			if !w.trayPresent && !w.visible {
				w.show()
			}
			return 0
		}
	}
	result, _, _ := defWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return result
}

func (w *desktopWindow) resize() {
	if w.controller == nil {
		return
	}
	var bounds windows.Rect
	if ok, _, _ := getClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&bounds))); ok != 0 {
		if err := webViewResult(w.controller.call(controllerSetBounds, uintptr(unsafe.Pointer(&bounds)))); err != nil {
			log.Printf("调整 Knooo 窗口大小：%v", err)
		}
	}
}

func (w *desktopWindow) show() {
	wasVisible := w.visible
	w.visible = true
	if !wasVisible {
		w.setWebViewMemoryTarget()
	}
	command := uintptr(windows.SW_SHOW)
	if iconic, _, _ := isIconic.Call(w.hwnd); iconic != 0 {
		command = windows.SW_RESTORE
	}
	showWindow.Call(w.hwnd, command)
	setForeground.Call(w.hwnd)
	if !w.webViewStarted {
		if err := w.startWebView(); err != nil {
			w.failWebView(err)
		}
	}
}

func (w *desktopWindow) addTrayIcon() {
	added, _, _ := shellNotifyIcon.Call(notifyAdd, uintptr(unsafe.Pointer(&w.tray)))
	w.trayPresent = added != 0
	if w.trayPresent {
		shellNotifyIcon.Call(notifyVersion, uintptr(unsafe.Pointer(&w.tray)))
	}
}

func (w *desktopWindow) showTrayMenu() {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	open := windows.StringToUTF16Ptr("打开 Knooo")
	quit := windows.StringToUTF16Ptr("退出 Knooo")
	appendMenu.Call(menu, 0, trayOpen, uintptr(unsafe.Pointer(open)))
	appendMenu.Call(menu, 0, trayQuit, uintptr(unsafe.Pointer(quit)))
	setMenuDefault.Call(menu, trayOpen, 0)
	var point windowPoint
	getCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	setForeground.Call(w.hwnd)
	command, _, _ := trackPopupMenu.Call(menu, 0x182, uintptr(point.x), uintptr(point.y), 0, w.hwnd, 0)
	postMessage.Call(w.hwnd, 0, 0, 0)
	switch command {
	case trayOpen:
		w.show()
	case trayQuit:
		postQuitMessage.Call(0)
	}
}
