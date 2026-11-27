package main

import (
	"log"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmActivate          = 0x0006
	wmPaint             = 0x000f
	wmDrawItem          = 0x002b
	wmNCCalcSize        = 0x0083
	wmNCHitTest         = 0x0084
	wmCommand           = 0x0111
	wmSystemCommand     = 0x0112
	wmDWMComposition    = 0x031e
	systemCommandPin    = 0x1f00
	pinButtonID         = 1
	captionBackground   = 0xfdfaf8 // #f8fafd, the sidebar background
	captionControlColor = 0x3b2b20
)

var (
	logicalToPhysical     = user32.NewProc("LogicalToPhysicalPointForPerMonitorDPI")
	physicalToLogical     = user32.NewProc("PhysicalToLogicalPointForPerMonitorDPI")
	dwmapi                = windows.NewLazySystemDLL("dwmapi.dll")
	uxtheme               = windows.NewLazySystemDLL("uxtheme.dll")
	gdi32                 = windows.NewLazySystemDLL("gdi32.dll")
	dwmDefWindowProc      = dwmapi.NewProc("DwmDefWindowProc")
	dwmExtendFrame        = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
	dwmGetAttribute       = dwmapi.NewProc("DwmGetWindowAttribute")
	dwmSetAttribute       = dwmapi.NewProc("DwmSetWindowAttribute")
	bufferedPaintInit     = uxtheme.NewProc("BufferedPaintInit")
	bufferedPaintUnInit   = uxtheme.NewProc("BufferedPaintUnInit")
	beginBufferedPaint    = uxtheme.NewProc("BeginBufferedPaint")
	bufferedPaintSetAlpha = uxtheme.NewProc("BufferedPaintSetAlpha")
	endBufferedPaint      = uxtheme.NewProc("EndBufferedPaint")
	beginPaint            = user32.NewProc("BeginPaint")
	endPaint              = user32.NewProc("EndPaint")
	fillRect              = user32.NewProc("FillRect")
	drawIconEx            = user32.NewProc("DrawIconEx")
	drawText              = user32.NewProc("DrawTextW")
	drawFocusRect         = user32.NewProc("DrawFocusRect")
	invalidateRect        = user32.NewProc("InvalidateRect")
	getWindowRect         = user32.NewProc("GetWindowRect")
	screenToClient        = user32.NewProc("ScreenToClient")
	getDPIForWindow       = user32.NewProc("GetDpiForWindow")
	getMetricsForDPI      = user32.NewProc("GetSystemMetricsForDpi")
	isZoomed              = user32.NewProc("IsZoomed")
	setWindowText         = user32.NewProc("SetWindowTextW")
	getSystemMenu         = user32.NewProc("GetSystemMenu")
	checkMenuItem         = user32.NewProc("CheckMenuItem")
	createSolidBrush      = gdi32.NewProc("CreateSolidBrush")
	createPen             = gdi32.NewProc("CreatePen")
	polyline              = gdi32.NewProc("Polyline")
	createFont            = gdi32.NewProc("CreateFontW")
	selectObject          = gdi32.NewProc("SelectObject")
	deleteObject          = gdi32.NewProc("DeleteObject")
	setBackgroundMode     = gdi32.NewProc("SetBkMode")
	setTextColor          = gdi32.NewProc("SetTextColor")
	getTextExtent         = gdi32.NewProc("GetTextExtentPoint32W")
)

type desktopTitleBar struct {
	pin, menu uintptr
	height    int32
	pinBounds windows.Rect
}

type titlePoint struct{ x, y int32 }

type paintStruct struct {
	dc        uintptr
	erase     int32
	rect      windows.Rect
	restore   int32
	increment int32
	reserved  [32]byte
}

type drawItemStruct struct {
	controlType uint32
	controlID   uint32
	itemID      uint32
	action      uint32
	state       uint32
	item        uintptr
	dc          uintptr
	rect        windows.Rect
	data        uintptr
}

func (w *desktopWindow) initTitleBar(module windows.Handle) {
	const (
		wsChild         = 0x40000000
		wsVisible       = 0x10000000
		bsOwnerDraw     = 0x0000000b
		swpFrameChanged = 0x0020
		swpNoActivate   = 0x0010
		swpNoZOrder     = 0x0004
		swpNoMoveNoSize = 0x0003
	)
	button, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("BUTTON"))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("窗口置顶"))),
		wsChild|wsVisible|bsOwnerDraw, 0, 0, 0, 0, w.hwnd, pinButtonID, uintptr(module), 0)
	if button == 0 {
		log.Printf("创建 Knooo 标题栏图钉：%v", err)
		return
	}
	hr, _, _ := bufferedPaintInit.Call()
	if err := webViewResult(hr); err != nil {
		destroyWindow.Call(button)
		log.Printf("初始化 Knooo 标题栏：%v", err)
		return
	}
	if err := w.extendTitleBar(); err != nil {
		bufferedPaintUnInit.Call()
		destroyWindow.Call(button)
		log.Printf("扩展 Knooo 标题栏：%v", err)
		return
	}
	w.titleBar.pin = button
	w.titleBar.menu, _, _ = getSystemMenu.Call(w.hwnd, 0)
	if w.titleBar.menu != 0 {
		appendMenu.Call(w.titleBar.menu, 0x0800, 0, 0) // MF_SEPARATOR
		appendMenu.Call(w.titleBar.menu, 0, systemCommandPin, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("窗口置顶"))))
	}
	w.url = strings.Replace(w.url, "/#", "/?desktop=1#", 1)
	setWindowPos.Call(w.hwnd, 0, 0, 0, 0, 0, swpFrameChanged|swpNoActivate|swpNoZOrder|swpNoMoveNoSize)
}

func (w *desktopWindow) extendTitleBar() error {
	// Keep DWM's caption buttons on the composited top edge.
	margins := [4]int32{0, 0, titlePixels(w.titleDPI(), 32), 0}
	caption, text := uint32(captionBackground), uint32(captionControlColor)
	dwmSetAttribute.Call(w.hwnd, 35, uintptr(unsafe.Pointer(&caption)), unsafe.Sizeof(caption)) // DWMWA_CAPTION_COLOR
	dwmSetAttribute.Call(w.hwnd, 36, uintptr(unsafe.Pointer(&text)), unsafe.Sizeof(text))       // DWMWA_TEXT_COLOR
	hr, _, _ := dwmExtendFrame.Call(w.hwnd, uintptr(unsafe.Pointer(&margins)))
	return webViewResult(hr)
}

func (w *desktopWindow) titleDPI() uintptr {
	dpi, _, _ := getDPIForWindow.Call(w.hwnd)
	return max(dpi, 96)
}

func titlePixels(dpi uintptr, pixels int32) int32 {
	return (int32(dpi)*pixels + 48) / 96
}

// DWM needs the full top edge in restored windows. Maximized windows keep
// their off-screen resize inset so their client area stays inside the monitor.
func (w *desktopWindow) titleClientTop() int32 {
	if zoomed, _, _ := isZoomed.Call(w.hwnd); zoomed != 0 {
		dpi := w.titleDPI()
		frame, _, _ := getMetricsForDPI.Call(33, dpi)   // SM_CYSIZEFRAME
		padding, _, _ := getMetricsForDPI.Call(92, dpi) // SM_CXPADDEDBORDER
		return int32(frame + padding)
	}
	return 0
}

func (w *desktopWindow) layoutTitleBar(client windows.Rect) {
	if w.titleBar.pin == 0 || client.Right == 0 || client.Bottom == 0 {
		return
	}
	dpi := w.titleDPI()
	width := titlePixels(dpi, 46)
	top, bottom := int32(0), titlePixels(dpi, 32)
	right := client.Right - 3*width
	if caption, ok := w.captionButtons(); ok {
		// DWM can mix coordinate scales for the caption band's position and size.
		right = client.Right - (caption.Right - caption.Left)
		top = max(0, caption.Top)
		bottom = top + caption.Bottom - caption.Top
	}
	right = max(0, right)
	pin := windows.Rect{Left: max(0, right-width), Top: top, Right: right, Bottom: bottom}
	height := max(bottom, titlePixels(dpi, 32))
	if pin != w.titleBar.pinBounds {
		w.titleBar.pinBounds = pin
		setWindowPos.Call(w.titleBar.pin, 0, uintptr(pin.Left), uintptr(pin.Top), uintptr(pin.Right-pin.Left), uintptr(pin.Bottom-pin.Top), 0x0014) // SWP_NOACTIVATE | SWP_NOZORDER
		area := windows.Rect{Right: client.Right, Bottom: height}
		invalidateRect.Call(w.hwnd, uintptr(unsafe.Pointer(&area)), 0)
	}
	w.titleBar.height = height
}

func (w *desktopWindow) captionButtons() (windows.Rect, bool) {
	var caption, outer windows.Rect
	const dwmCaptionButtonBounds = 5
	hr, _, _ := dwmGetAttribute.Call(w.hwnd, dwmCaptionButtonBounds, uintptr(unsafe.Pointer(&caption)), unsafe.Sizeof(caption))
	if int32(hr) < 0 || caption.Right <= caption.Left || caption.Bottom <= caption.Top {
		return windows.Rect{}, false
	}
	if ok, _, _ := getWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&outer))); ok == 0 {
		return windows.Rect{}, false
	}

	// DWM reports physical pixels, while the HWND and WebView use logical coordinates.
	origin := titlePoint{outer.Left, outer.Top}
	if ok, _, _ := logicalToPhysical.Call(w.hwnd, uintptr(unsafe.Pointer(&origin))); ok == 0 {
		return windows.Rect{}, false
	}
	first := titlePoint{origin.x + caption.Left, origin.y + caption.Top}
	last := titlePoint{origin.x + caption.Right, origin.y + caption.Bottom}
	for _, point := range []*titlePoint{&first, &last} {
		if ok, _, _ := physicalToLogical.Call(w.hwnd, uintptr(unsafe.Pointer(point))); ok == 0 {
			return windows.Rect{}, false
		}
		if ok, _, _ := screenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(point))); ok == 0 {
			return windows.Rect{}, false
		}
	}
	return windows.Rect{Left: first.x, Top: first.y, Right: last.x, Bottom: last.y}, true
}

func (w *desktopWindow) titleHitTest(hwnd, wParam, lParam uintptr) uintptr {
	const (
		htClient  = 1
		htCaption = 2
	)
	var result uintptr
	if handled, _, _ := dwmDefWindowProc.Call(hwnd, wmNCHitTest, wParam, lParam, uintptr(unsafe.Pointer(&result))); handled == 0 {
		result, _, _ = defWindowProc.Call(hwnd, wmNCHitTest, wParam, lParam)
	}
	// Leave resize edges and the system caption menu to Windows.
	if result != htClient && result != htCaption {
		return result
	}
	point := titlePoint{int32(int16(uint16(lParam))), int32(int16(uint16(lParam >> 16)))}
	if ok, _, _ := screenToClient.Call(hwnd, uintptr(unsafe.Pointer(&point))); ok == 0 || point.y < 0 || point.y >= w.titleBar.height {
		return result
	}
	pin := w.titleBar.pinBounds
	if point.y >= pin.Top && point.y < pin.Bottom {
		if point.x >= pin.Left && point.x < pin.Right {
			return htClient
		}
		if point.x >= pin.Right {
			if zoomed, _, _ := isZoomed.Call(hwnd); zoomed != 0 {
				var client windows.Rect
				getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))
				width := titlePixels(w.titleDPI(), 46)
				left := client.Right - 3*width
				if point.x >= left && point.x < client.Right {
					// DWM paints these controls but can miss their hits with a maximized resize inset.
					hits := [...]uintptr{8, 9, 20} // HTMINBUTTON, HTMAXBUTTON, HTCLOSE
					return hits[(point.x-left)/width]
				}
			}
		}
	}
	return htCaption
}

func (w *desktopWindow) toggleTitlePin() {
	command := topmostOn
	if windowTopmost(w.hwnd, topmostQuery) == topmostOn {
		command = topmostOff
	}
	if w.setTitlePin(command) == 0 {
		log.Print("Windows 窗口未能读取或修改置顶状态")
	}
}

func (w *desktopWindow) setTitlePin(command uintptr) uintptr {
	state := windowTopmost(w.hwnd, command)
	if w.titleBar.pin != 0 && state != 0 && command != topmostQuery {
		label, menuFlags := "窗口置顶", uintptr(0)
		if state == topmostOn {
			label, menuFlags = "取消窗口置顶", 0x0008 // MF_CHECKED
		}
		setWindowText.Call(w.titleBar.pin, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(label))))
		invalidateRect.Call(w.titleBar.pin, 0, 0)
		if w.titleBar.menu != 0 {
			checkMenuItem.Call(w.titleBar.menu, systemCommandPin, menuFlags)
		}
	}
	return state
}

func (w *desktopWindow) paintTitleBar() {
	var ps paintStruct
	dc, _, _ := beginPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	defer endPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	if dc == 0 || ps.rect.Top >= w.titleBar.height {
		return
	}
	area := windows.Rect{Right: w.titleBar.pinBounds.Right, Bottom: w.titleBar.height}
	system := windows.Rect{Left: area.Right, Right: ps.rect.Right, Bottom: area.Bottom}
	fillTitleRect(dc, &system, 0)
	dc, finish := opaqueTitlePaint(dc, &area)
	defer finish()
	fillTitleRect(dc, &area, captionBackground)
	dpi := w.titleDPI()
	size := titlePixels(dpi, 23)
	left := titlePixels(dpi, 13)
	top := (area.Bottom - size) / 2
	if w.titleBar.pinBounds.Left < titlePixels(dpi, 115) {
		return
	}
	drawIconEx.Call(dc, uintptr(left), uintptr(top), w.tray.icon, uintptr(size), uintptr(size), 0, 0, 3) // DI_NORMAL
	restoreFont := titleFont(dc, "Segoe UI", titlePixels(dpi, 19), 700)
	defer restoreFont()
	setBackgroundMode.Call(dc, 1) // TRANSPARENT
	area.Left = left + size + titlePixels(dpi, 6)
	colors := [...]uintptr{0xa85d22, 0x353dbd, 0x095b88, 0x4f7528, 0xa95753}
	for i, ch := range "Knooo" {
		glyph := [2]uint16{uint16(ch)}
		setTextColor.Call(dc, colors[i])
		drawText.Call(dc, uintptr(unsafe.Pointer(&glyph)), 1, uintptr(unsafe.Pointer(&area)), 0x24) // DT_SINGLELINE | DT_VCENTER
		var extent titlePoint
		getTextExtent.Call(dc, uintptr(unsafe.Pointer(&glyph)), 1, uintptr(unsafe.Pointer(&extent)))
		area.Left += extent.x
	}
}

func (w *desktopWindow) paintTitlePin(item *drawItemStruct) {
	const (
		odsSelected = 0x0001
		odsFocus    = 0x0010
		odsHotLight = 0x0040
	)
	pinned := windowTopmost(w.hwnd, topmostQuery) == topmostOn
	background, foreground := uintptr(captionBackground), uintptr(captionControlColor)
	if pinned {
		background, foreground = 0xfdeee0, 0x9e5c21
	}
	if item.state&(odsSelected|odsHotLight) != 0 {
		background = 0xf6ece4
	}
	dc, finish := opaqueTitlePaint(item.dc, &item.rect)
	defer finish()
	fillTitleRect(dc, &item.rect, background)
	w.drawPinGlyph(dc, item.rect, foreground)
	if item.state&odsFocus != 0 {
		focus := item.rect
		focus.Left, focus.Top = focus.Left+3, focus.Top+3
		focus.Right, focus.Bottom = focus.Right-3, focus.Bottom-3
		drawFocusRect.Call(dc, uintptr(unsafe.Pointer(&focus)))
	}
}

func (w *desktopWindow) drawPinGlyph(dc uintptr, rect windows.Rect, color uintptr) {
	dpi := w.titleDPI()
	x := (rect.Left+rect.Right)/2 - titlePixels(dpi, 4)
	y := (rect.Top+rect.Bottom)/2 - titlePixels(dpi, 7)
	pen, _, _ := createPen.Call(0, uintptr(titlePixels(dpi, 1)), color)
	if pen == 0 {
		return
	}
	previous, _, _ := selectObject.Call(dc, pen)
	body := [...]titlePoint{{1, 0}, {7, 0}, {6, 3}, {6, 6}, {8, 8}, {8, 9}, {0, 9}, {0, 8}, {2, 6}, {2, 3}, {1, 0}}
	needle := [...]titlePoint{{4, 9}, {4, 14}}
	for _, points := range [][]titlePoint{body[:], needle[:]} {
		for i, point := range points {
			points[i] = titlePoint{x + titlePixels(dpi, point.x), y + titlePixels(dpi, point.y)}
		}
		polyline.Call(dc, uintptr(unsafe.Pointer(&points[0])), uintptr(len(points)))
	}
	selectObject.Call(dc, previous)
	deleteObject.Call(pen)
}

// GDI leaves alpha zero in an extended DWM frame.
func opaqueTitlePaint(dc uintptr, rect *windows.Rect) (uintptr, func()) {
	const bpbfTopDownDIB = 2
	var bufferDC uintptr
	buffer, _, _ := beginBufferedPaint.Call(dc, uintptr(unsafe.Pointer(rect)), bpbfTopDownDIB, 0, uintptr(unsafe.Pointer(&bufferDC)))
	if buffer == 0 {
		return dc, func() {}
	}
	return bufferDC, func() {
		bufferedPaintSetAlpha.Call(buffer, 0, 255)
		endBufferedPaint.Call(buffer, 1)
	}
}

func titleFont(dc uintptr, family string, height int32, weight uintptr) func() {
	font, _, _ := createFont.Call(uintptr(-height), 0, 0, 0, weight, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(family))))
	previous, _, _ := selectObject.Call(dc, font)
	return func() {
		selectObject.Call(dc, previous)
		deleteObject.Call(font)
	}
}

func fillTitleRect(dc uintptr, rect *windows.Rect, color uintptr) {
	brush, _, _ := createSolidBrush.Call(color)
	fillRect.Call(dc, uintptr(unsafe.Pointer(rect)), brush)
	deleteObject.Call(brush)
}
