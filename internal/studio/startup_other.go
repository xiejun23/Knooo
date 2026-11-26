//go:build !windows

package studio

func windowsDesktopStartup(*bool) (desktopStartupState, error) {
	return startupUnavailable, nil
}
