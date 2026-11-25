//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

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
