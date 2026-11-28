package main

import (
	"math"
	"testing"

	"golang.org/x/sys/windows"
)

func TestResolveCaptionBand(t *testing.T) {
	client := windows.Rect{Right: 1200, Bottom: 800}
	caption := windows.Rect{Left: 1062, Top: 1, Right: 1202, Bottom: 31}
	band, height := resolveCaptionBand(client, 96, caption, true)
	if band.Left != 1062 || band.Right != 1200 {
		t.Fatalf("band horizontal = [%d,%d), want [1062,1200)", band.Left, band.Right)
	}
	if band.Top != 1 || band.Bottom != 31 {
		t.Fatalf("band vertical = [%d,%d), want [1,31)", band.Top, band.Bottom)
	}
	if height != 32 {
		t.Fatalf("height = %d, want 32", height)
	}

	// Fallback without DWM caption bounds at 150% DPI.
	band, height = resolveCaptionBand(client, 144, windows.Rect{}, false)
	if band.Left != client.Right-3*69 {
		t.Fatalf("150%% DPI fallback left = %d, want %d", band.Left, client.Right-3*69)
	}
	if height != 48 {
		t.Fatalf("150%% DPI fallback height = %d, want 48", height)
	}
}

func TestEagleTakeoffPose(t *testing.T) {
	if got := eagleTakeoffPose(0); got != (brandPose{}) {
		t.Fatalf("pose(0) = %+v, want zero", got)
	}
	if got := eagleTakeoffPose(1); got != (brandPose{}) {
		t.Fatalf("pose(1) = %+v, want zero", got)
	}
	peak := eagleTakeoffPose(0.35)
	if math.Abs(peak.dxDIP-4) > 1e-6 || math.Abs(peak.dyDIP+4) > 1e-6 || math.Abs(peak.angleDeg+12) > 1e-6 {
		t.Fatalf("pose(0.35) = %+v, want {4 -4 -12}", peak)
	}
	glide := eagleTakeoffPose(0.68)
	if math.Abs(glide.dxDIP+2) > 1e-6 || math.Abs(glide.dyDIP+2) > 1e-6 || math.Abs(glide.angleDeg-7) > 1e-6 {
		t.Fatalf("pose(0.68) = %+v, want {-2 -2 7}", glide)
	}
}

func TestLoadAndRenderTitleEagle(t *testing.T) {
	eagle := loadTitleEagle()
	if eagle == nil {
		t.Fatal("expected embedded eagle PNG to load")
	}
	if eagle.Bounds().Dx() != 64 || eagle.Bounds().Dy() != 64 {
		t.Fatalf("downsampled eagle = %v, want 64x64", eagle.Bounds())
	}
	buf := renderTitleEagleBGRA(eagle, 39, 32, 19.5, 16, 23, -12)
	if len(buf) != 39*32*4 {
		t.Fatalf("buffer len = %d, want %d", len(buf), 39*32*4)
	}
	var changed bool
	for i := 0; i < len(buf); i += 4 {
		if buf[i+3] != 255 {
			t.Fatalf("pixel alpha at %d = %d, want 255", i, buf[i+3])
		}
		if buf[i] != 0xfd || buf[i+1] != 0xfa || buf[i+2] != 0xf8 {
			changed = true
		}
	}
	if !changed {
		t.Fatal("rendered eagle image was blank")
	}
}
