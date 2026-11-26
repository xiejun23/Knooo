package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func RegisteredExecutable() string {
	if runtime.GOARCH != "amd64" {
		return ""
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\KnoooStudio`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	dir, _, err := key.GetStringValue("InstallLocation")
	if err != nil || !filepath.IsAbs(dir) {
		return ""
	}
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	current, err := os.Stat(path)
	if err != nil {
		return ""
	}
	registeredPath := filepath.Join(dir, "Knooo.exe")
	registered, err := os.Stat(registeredPath)
	if err != nil || !os.SameFile(current, registered) {
		return ""
	}
	return registeredPath
}

func launchInstaller(path string) error {
	cmd := exec.Command(path, "/UPDATEPID="+strconv.Itoa(os.Getpid()))
	cmd.Dir = filepath.Dir(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}
