//go:build !windows

package update

import "errors"

func RegisteredExecutable() string { return "" }

func launchInstaller(string) error {
	return errors.New("one-click updates require the Windows installer")
}
