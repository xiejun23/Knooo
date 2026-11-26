package studio

import (
	"encoding/binary"
	"errors"
	"strings"

	"github.com/xiejun23/Knooo/internal/update"
	"golang.org/x/sys/windows/registry"
)

const (
	startupRunKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
	startupApprovalKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
	startupValue       = "KnoooStudio"
)

func windowsDesktopStartup(enabled *bool) (desktopStartupState, error) {
	executable := update.RegisteredExecutable()
	if executable == "" {
		return startupUnavailable, nil
	}
	command := `"` + executable + `" --tray`
	if enabled != nil && *enabled {
		state, err := readStartupApproval()
		if err != nil {
			return "", err
		}
		key, _, err := registry.CreateKey(registry.CURRENT_USER, startupRunKey, registry.SET_VALUE)
		if err != nil {
			return "", err
		}
		defer key.Close()
		return state, key.SetStringValue(startupValue, command)
	}

	access := uint32(registry.QUERY_VALUE)
	if enabled != nil {
		access = registry.SET_VALUE
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, startupRunKey, access)
	if errors.Is(err, registry.ErrNotExist) {
		return startupOff, nil
	}
	if err != nil {
		return "", err
	}
	defer key.Close()

	if enabled != nil {
		err := key.DeleteValue(startupValue)
		if errors.Is(err, registry.ErrNotExist) {
			err = nil
		}
		return startupOff, err
	}

	value, valueType, err := key.GetStringValue(startupValue)
	if errors.Is(err, registry.ErrNotExist) || errors.Is(err, registry.ErrUnexpectedType) {
		return startupOff, nil
	}
	if err != nil {
		return "", err
	}
	if valueType != registry.SZ || !strings.EqualFold(value, command) {
		return startupOff, nil
	}
	return readStartupApproval()
}

func readStartupApproval() (desktopStartupState, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, startupApprovalKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return startupOn, nil
	}
	if err != nil {
		return "", err
	}
	defer key.Close()

	value, _, err := key.GetBinaryValue(startupValue)
	if errors.Is(err, registry.ErrNotExist) {
		return startupOn, nil
	}
	if err != nil {
		return "", err
	}
	if len(value) < 4 {
		return "", errors.New("invalid Windows startup approval")
	}
	// Explorer stores the disabled flag in the lowest bit of its approval state.
	if binary.LittleEndian.Uint32(value)&1 != 0 {
		return startupBlocked, nil
	}
	return startupOn, nil
}
