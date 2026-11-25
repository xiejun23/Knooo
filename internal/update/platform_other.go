//go:build !windows

package update

import "errors"

func installed() bool { return false }

func launchInstaller(string) error {
	return errors.New("one-click updates require the Windows installer")
}
