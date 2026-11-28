package main

import (
	"image"
	"image/draw"
	"image/png"
	"io/fs"
	"log"
	"math"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmActivate          = 0x0006
	wmPaint             = 0x000f
	wmNCCalcSize        = 0x0083
	wmNCHitTest         = 0x0084
	wmNCMouseMove       = 0x00a0
	wmSystemCommand     = 0x0112
	wmNCMouseLeave      = 0x02a2
	wmDWMComposition    = 0x031e
	systemCommandPin    = 0x1f00
	brandFlightTimer    = 2
	brandFlightDuration = 1200 * time.Millisecond
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
	invalidateRect        = user32.NewProc("InvalidateRect")
	getWindowRect         = user32.NewProc("GetWindowRect")
	screenToClient        = user32.NewProc("ScreenToClient")
	getDPIForWindow       = user32.NewProc("GetDpiForWindow")
	getMetricsForDPI      = user32.NewProc("GetSystemMetricsForDpi")
	isZoomed              = user32.NewProc("IsZoomed")
	getSystemMenu         = user32.NewProc("GetSystemMenu")
	checkMenuItem         = user32.NewProc("CheckMenuItem")
	trackMouseEvent       = user32.NewProc("TrackMouseEvent")
	createSolidBrush      = gdi32.NewProc("CreateSolidBrush")
	createFont            = gdi32.NewProc("CreateFontW")
	selectObject          = gdi32.NewProc("SelectObject")
	deleteObject          = gdi32.NewProc("DeleteObject")
	setBackgroundMode     = gdi32.NewProc("SetBkMode")
	setTextColor          = gdi32.NewProc("SetTextColor")
	getTextExtent         = gdi32.NewProc("GetTextExtentPoint32W")
	setDIBitsToDevice     = gdi32.NewProc("SetDIBitsToDevice")
)

type desktopTitleBar struct {
	enabled      bool
	menu         uintptr
	height       int32
	captionBand  windows.Rect
	brandBounds  windows.Rect
	brandHovered bool
	trackingNC   bool
	brandFlying  bool
	flightStart  time.Time
	eagle        *image.NRGBA
}

type titlePoint struct{ x, y int32 }

type trackMouseEventStruct struct {
	size      uint32
	flags     uint32
	hwnd      uintptr
	hoverTime uint32
}

type bitmapInfoHeader struct {
	size          uint32
	width         int32
	height        int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	imageSize     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

type paintStruct struct {
	dc        uintptr
	erase     int32
	rect      windows.Rect
	restore   int32
	increment int32
	reserved  [32]byte
}

type brandPose struct {
	dxDIP    float64
	dyDIP    float64
	angleDeg float64
}

func enablePerMonitorDPI() {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4) keeps the titlebar, eagle icon,
	// and WebView crisp at native monitor resolution alongside DWM's caption buttons.
	const perMonitorAwareV2 = ^uintptr(3)
	if err := setProcessDPIAwareness.Find(); err == nil {
		if ok, _, _ := setProcessDPIAwareness.Call(perMonitorAwareV2); ok != 0 {
			return
		}
	}
	if err := setProcessDPIAwareLegacy.Find(); err == nil {
		setProcessDPIAwareLegacy.Call()
	}
}

func (w *desktopWindow) initTitleBar() {
	const (
		swpFrameChanged = 0x0020
		swpNoActivate   = 0x0010
		swpNoZOrder     = 0x0004
		swpNoMoveNoSize = 0x0003
	)
	hr, _, _ := bufferedPaintInit.Call()
	if err := webViewResult(hr); err != nil {
		log.Printf("初始化 Knooo 标题栏：%v", err)
		return
	}
	if err := w.extendTitleBar(); err != nil {
		bufferedPaintUnInit.Call()
		log.Printf("扩展 Knooo 标题栏：%v", err)
		return
	}
	w.titleBar.enabled = true
	w.titleBar.eagle = loadTitleEagle()
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

func resolveCaptionBand(client windows.Rect, dpi uintptr, caption windows.Rect, hasCaption bool) (windows.Rect, int32) {
	defaultWidth := titlePixels(dpi, 46)
	defaultHeight := titlePixels(dpi, 32)
	top, bottom := int32(0), defaultHeight
	left := max(0, client.Right-3*defaultWidth)

	if hasCaption {
		bandHeight := caption.Bottom - caption.Top
		if bandHeight >= titlePixels(dpi, 20) && bandHeight <= titlePixels(dpi, 48) {
			top = max(0, caption.Top)
			bottom = top + bandHeight
		}
		bandWidth := caption.Right - caption.Left
		minBand := 3 * titlePixels(dpi, 36)
		maxBand := 3 * titlePixels(dpi, 56)
		tolerance := titlePixels(dpi, 8)
		if bandWidth >= minBand && bandWidth <= maxBand {
			if caption.Right >= client.Right-tolerance && caption.Right <= client.Right+tolerance &&
				caption.Left >= 0 && caption.Left <= client.Right-minBand {
				left = caption.Left
			} else {
				left = max(0, client.Right-bandWidth)
			}
		}
	}

	band := windows.Rect{Left: left, Top: top, Right: client.Right, Bottom: bottom}
	height := max(bottom, defaultHeight)
	return band, height
}

func (w *desktopWindow) layoutTitleBar(client windows.Rect) {
	if !w.titleBar.enabled || client.Right == 0 || client.Bottom == 0 {
		return
	}
	dpi := w.titleDPI()
	caption, ok := w.captionButtons()
	band, height := resolveCaptionBand(client, dpi, caption, ok)
	if band != w.titleBar.captionBand || height != w.titleBar.height {
		w.titleBar.captionBand = band
		w.titleBar.height = height
		area := windows.Rect{Right: client.Right, Bottom: height}
		invalidateRect.Call(w.hwnd, uintptr(unsafe.Pointer(&area)), 0)
	}
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

	// DWM reports physical pixels, while the HWND and WebView may use logical coordinates.
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
	band := w.titleBar.captionBand
	if point.y >= band.Top && point.y < band.Bottom && point.x >= band.Left {
		if zoomed, _, _ := isZoomed.Call(hwnd); zoomed != 0 {
			var client windows.Rect
			getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))
			span := client.Right - band.Left
			if span > 0 && point.x < client.Right {
				// DWM paints these controls but can miss their hits with a maximized resize inset.
				hits := [...]uintptr{8, 9, 20} // HTMINBUTTON, HTMAXBUTTON, HTCLOSE
				index := min(2, int((point.x-band.Left)*3/span))
				return hits[index]
			}
		}
	}
	return htCaption
}

func (w *desktopWindow) handleTitleMouseMove(hwnd, lParam uintptr) {
	const tmeNonClientLeave = 0x0012 // TME_NONCLIENT | TME_LEAVE
	if !w.titleBar.trackingNC {
		tme := trackMouseEventStruct{size: uint32(unsafe.Sizeof(trackMouseEventStruct{})), flags: tmeNonClientLeave, hwnd: hwnd}
		trackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
		w.titleBar.trackingNC = true
	}
	point := titlePoint{int32(int16(uint16(lParam))), int32(int16(uint16(lParam >> 16)))}
	if ok, _, _ := screenToClient.Call(hwnd, uintptr(unsafe.Pointer(&point))); ok == 0 {
		return
	}
	overBrand := pointInRect(point, w.titleBar.brandBounds)
	if overBrand && !w.titleBar.brandHovered {
		w.titleBar.brandHovered = true
		w.startBrandFlight()
	} else if !overBrand && w.titleBar.brandHovered {
		w.titleBar.brandHovered = false
	}
}

func (w *desktopWindow) handleTitleMouseLeave() {
	w.titleBar.trackingNC = false
	w.titleBar.brandHovered = false
}

func (w *desktopWindow) startBrandFlight() {
	w.titleBar.flightStart = time.Now()
	w.titleBar.brandFlying = true
	setTimer.Call(w.hwnd, brandFlightTimer, 16, 0)
	w.invalidateBrandArea()
}

func (w *desktopWindow) stepBrandFlight() {
	if !w.titleBar.brandFlying {
		killTimer.Call(w.hwnd, brandFlightTimer)
		return
	}
	if time.Since(w.titleBar.flightStart) >= brandFlightDuration {
		w.titleBar.brandFlying = false
		killTimer.Call(w.hwnd, brandFlightTimer)
	}
	w.invalidateBrandArea()
}

func (w *desktopWindow) invalidateBrandArea() {
	right := w.titleBar.brandBounds.Right
	if right <= 0 {
		right = w.titleBar.captionBand.Left
	}
	if right <= 0 || w.titleBar.height <= 0 {
		return
	}
	area := windows.Rect{Right: min(right, w.titleBar.captionBand.Left), Bottom: w.titleBar.height}
	invalidateRect.Call(w.hwnd, uintptr(unsafe.Pointer(&area)), 0)
}

func (w *desktopWindow) brandFlightPose(now time.Time) brandPose {
	if !w.titleBar.brandFlying {
		return brandPose{}
	}
	progress := float64(now.Sub(w.titleBar.flightStart)) / float64(brandFlightDuration)
	return eagleTakeoffPose(progress)
}

func eagleTakeoffPose(progress float64) brandPose {
	if progress <= 0 || progress >= 1 {
		return brandPose{}
	}
	type keyframe struct {
		t    float64
		pose brandPose
	}
	frames := [...]keyframe{
		{0.00, brandPose{0, 0, 0}},
		{0.35, brandPose{4, -4, -12}},
		{0.68, brandPose{-2, -2, 7}},
		{1.00, brandPose{0, 0, 0}},
	}
	for i := 1; i < len(frames); i++ {
		if progress <= frames[i].t {
			a, b := frames[i-1], frames[i]
			u := (progress - a.t) / (b.t - a.t)
			// Smoothstep ease-in-out between keyframes.
			e := u * u * (3 - 2*u)
			return brandPose{
				dxDIP:    a.pose.dxDIP + (b.pose.dxDIP-a.pose.dxDIP)*e,
				dyDIP:    a.pose.dyDIP + (b.pose.dyDIP-a.pose.dyDIP)*e,
				angleDeg: a.pose.angleDeg + (b.pose.angleDeg-a.pose.angleDeg)*e,
			}
		}
	}
	return brandPose{}
}

func pointInRect(p titlePoint, r windows.Rect) bool {
	return p.x >= r.Left && p.x < r.Right && p.y >= r.Top && p.y < r.Bottom
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
	if w.titleBar.enabled && state != 0 && command != topmostQuery && w.titleBar.menu != 0 {
		menuFlags := uintptr(0)
		if state == topmostOn {
			menuFlags = 0x0008 // MF_CHECKED
		}
		checkMenuItem.Call(w.titleBar.menu, systemCommandPin, menuFlags)
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
	area := windows.Rect{Right: w.titleBar.captionBand.Left, Bottom: w.titleBar.height}
	system := windows.Rect{Left: area.Right, Right: ps.rect.Right, Bottom: area.Bottom}
	fillTitleRect(dc, &system, 0)
	dc, finish := opaqueTitlePaint(dc, &area)
	defer finish()
	fillTitleRect(dc, &area, captionBackground)
	dpi := w.titleDPI()
	size := titlePixels(dpi, 23)
	left := titlePixels(dpi, 13)
	top := (area.Bottom - size) / 2
	if w.titleBar.captionBand.Left < titlePixels(dpi, 115) {
		w.titleBar.brandBounds = windows.Rect{}
		return
	}
	pose := w.brandFlightPose(time.Now())
	if w.titleBar.eagle != nil {
		drawTitleEagle(dc, w.titleBar.eagle, left, top, size, w.titleBar.captionBand.Left, area.Bottom, dpi, pose)
	} else {
		dx := int32(math.Round(pose.dxDIP * float64(dpi) / 96))
		dy := int32(math.Round(pose.dyDIP * float64(dpi) / 96))
		drawIconEx.Call(dc, uintptr(left+dx), uintptr(top+dy), w.tray.icon, uintptr(size), uintptr(size), 0, 0, 3) // DI_NORMAL
	}
	restoreFont := titleFont(dc, "Segoe UI", titlePixels(dpi, 19), 700, 0)
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
	w.titleBar.brandBounds = windows.Rect{
		Left:   max(0, left-titlePixels(dpi, 4)),
		Top:    0,
		Right:  min(w.titleBar.captionBand.Left, area.Left+titlePixels(dpi, 6)),
		Bottom: w.titleBar.height,
	}
}

func loadTitleEagle() *image.NRGBA {
	matches, err := fs.Glob(assets, "ui/dist/assets/eagle-*.png")
	if err != nil || len(matches) == 0 {
		return nil
	}
	file, err := assets.Open(matches[0])
	if err != nil {
		return nil
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		return nil
	}
	bounds := decoded.Bounds()
	src := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(src, src.Bounds(), decoded, bounds.Min, draw.Src)
	return downsampleNRGBA(src, 64, 64)
}

func downsampleNRGBA(src *image.NRGBA, dstW, dstH int) *image.NRGBA {
	srcW, srcH := src.Bounds().Dx(), src.Bounds().Dy()
	if srcW <= dstW && srcH <= dstH {
		return src
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		y0 := y * srcH / dstH
		y1 := max(y0+1, (y+1)*srcH/dstH)
		for x := 0; x < dstW; x++ {
			x0 := x * srcW / dstW
			x1 := max(x0+1, (x+1)*srcW/dstW)
			var rSum, gSum, bSum, aSum uint64
			var count uint64
			for sy := y0; sy < y1; sy++ {
				row := sy * src.Stride
				for sx := x0; sx < x1; sx++ {
					off := row + sx*4
					a := uint64(src.Pix[off+3])
					rSum += uint64(src.Pix[off]) * a
					gSum += uint64(src.Pix[off+1]) * a
					bSum += uint64(src.Pix[off+2]) * a
					aSum += a
					count++
				}
			}
			off := y*dst.Stride + x*4
			if aSum > 0 {
				dst.Pix[off] = uint8((rSum + aSum/2) / aSum)
				dst.Pix[off+1] = uint8((gSum + aSum/2) / aSum)
				dst.Pix[off+2] = uint8((bSum + aSum/2) / aSum)
				dst.Pix[off+3] = uint8((aSum + count/2) / count)
			}
		}
	}
	return dst
}

func renderTitleEagleBGRA(src *image.NRGBA, boxW, boxH int, centerX, centerY, size, angleDeg float64) []byte {
	const (
		bgR = float64(captionBackground & 0xff)
		bgG = float64((captionBackground >> 8) & 0xff)
		bgB = float64((captionBackground >> 16) & 0xff)
	)
	pixels := make([]byte, boxW*boxH*4)
	rad := angleDeg * math.Pi / 180
	cosA, sinA := math.Cos(rad), math.Sin(rad)
	srcW, srcH := float64(src.Bounds().Dx()), float64(src.Bounds().Dy())
	half := size / 2
	scaleX, scaleY := srcW/size, srcH/size

	for py := 0; py < boxH; py++ {
		oy := float64(py) + 0.5 - centerY
		rowOff := py * boxW * 4
		for px := 0; px < boxW; px++ {
			ox := float64(px) + 0.5 - centerX
			ux := ox*cosA + oy*sinA
			uy := -ox*sinA + oy*cosA
			sx := (ux+half)*scaleX - 0.5
			sy := (uy+half)*scaleY - 0.5
			r, g, b, a := sampleBilinearNRGBA(src, sx, sy)
			inv := 1 - a
			outR := uint8(math.Round(r*a + bgR*inv))
			outG := uint8(math.Round(g*a + bgG*inv))
			outB := uint8(math.Round(b*a + bgB*inv))
			off := rowOff + px*4
			pixels[off] = outB
			pixels[off+1] = outG
			pixels[off+2] = outR
			pixels[off+3] = 255
		}
	}
	return pixels
}

func sampleBilinearNRGBA(src *image.NRGBA, x, y float64) (r, g, b, a float64) {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if x <= -1 || y <= -1 || x >= float64(w) || y >= float64(h) {
		return 0, 0, 0, 0
	}
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := x - float64(x0)
	fy := y - float64(y0)
	weights := [4]float64{
		(1 - fx) * (1 - fy),
		fx * (1 - fy),
		(1 - fx) * fy,
		fx * fy,
	}
	coords := [4][2]int{{x0, y0}, {x0 + 1, y0}, {x0, y0 + 1}, {x0 + 1, y0 + 1}}
	var pr, pg, pb, pa float64
	for i, c := range coords {
		cx, cy := c[0], c[1]
		if cx < 0 || cy < 0 || cx >= w || cy >= h {
			continue
		}
		off := cy*src.Stride + cx*4
		alpha := float64(src.Pix[off+3]) / 255
		wt := weights[i] * alpha
		pr += float64(src.Pix[off]) * wt
		pg += float64(src.Pix[off+1]) * wt
		pb += float64(src.Pix[off+2]) * wt
		pa += wt
	}
	if pa <= 1e-6 {
		return 0, 0, 0, 0
	}
	return pr / pa, pg / pa, pb / pa, pa
}

func drawTitleEagle(dc uintptr, src *image.NRGBA, left, top, size, maxRight, barHeight int32, dpi uintptr, pose brandPose) {
	pad := titlePixels(dpi, 8)
	boxLeft := max(0, left-pad)
	boxRight := min(maxRight, left+size+pad)
	boxW, boxH := int(boxRight-boxLeft), int(barHeight)
	if boxW <= 0 || boxH <= 0 {
		return
	}
	dxPx := pose.dxDIP * float64(dpi) / 96
	dyPx := pose.dyDIP * float64(dpi) / 96
	cx := float64(left-boxLeft) + float64(size)/2 + dxPx
	cy := float64(top) + float64(size)/2 + dyPx
	pixels := renderTitleEagleBGRA(src, boxW, boxH, cx, cy, float64(size), pose.angleDeg)
	header := bitmapInfoHeader{
		size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		width:       int32(boxW),
		height:      -int32(boxH), // top-down DIB
		planes:      1,
		bitCount:    32,
		compression: 0, // BI_RGB
	}
	setDIBitsToDevice.Call(
		dc,
		uintptr(boxLeft), 0,
		uintptr(boxW), uintptr(boxH),
		0, 0,
		0, uintptr(boxH),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&header)),
		0, // DIB_RGB_COLORS
	)
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

func titleFont(dc uintptr, family string, height int32, weight, quality uintptr) func() {
	font, _, _ := createFont.Call(uintptr(-height), 0, 0, 0, weight, 0, 0, 0, 1, 0, 0, quality, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(family))))
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
