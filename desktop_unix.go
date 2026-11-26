//go:build !windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
)

type desktopHost struct{}

func newDesktopHost(_, tray bool) (*desktopHost, error) {
	if tray {
		return nil, fmt.Errorf("--tray 仅支持 Windows")
	}
	return &desktopHost{}, nil
}

func (*desktopHost) close() {}

func showDesktopError(error) {}

func (*desktopHost) run(url, _ string, stop <-chan struct{}) error {
	go func() {
		if err := openDesktop(url); err != nil {
			log.Print(err)
		}
	}()
	<-stop
	return nil
}

func openDesktop(url string) error {
	opener := "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		opener = "open"
	case "linux":
		if os.Getenv("WSL_DISTRO_NAME") != "" {
			if err := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Run(); err == nil {
				return nil
			}
		}
	}
	if err := exec.Command(opener, url).Run(); err != nil {
		return fmt.Errorf("open default browser with %s: %w", opener, err)
	}
	return nil
}
