package sessions

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsPathsOnWSL(t *testing.T) {
	mountInfo := `79 82 0:36 / /usr/lib/wsl/drivers ro - 9p drivers ro,aname=drivers
132 82 0:70 / /drives/main\040drive rw - 9p C:\134 rw,aname=drvfs;path=C:\;uid=1000
133 82 0:71 / /drives/work rw - drvfs D:\134 rw
134 82 0:71 /shared /worktree rw - drvfs D:\134 rw
`
	mounts := readWindowsMounts(bufio.NewScanner(strings.NewReader(mountInfo)))
	if len(mounts) != 3 {
		t.Fatalf("found %d Windows mounts, want 3", len(mounts))
	}
	if runtime.GOOS == "windows" {
		return
	}
	for native, local := range map[string]string{
		`c:\Users\示例`:          "/drives/main drive/Users/示例",
		`D:/src/../project`:    "/drives/work/project",
		`D:\shared\project`:    "/worktree/project",
		`D:\shared-other\src`:  "/drives/work/shared-other/src",
		`Z:\unmounted`:         "",
		`C:relative`:           "",
		`/drives/work/project`: "",
	} {
		if got := mounts.localPath(native); got != local {
			t.Errorf("localPath(%q) = %q, want %q", native, got, local)
		}
	}
	if got := mounts.nativePath("/worktree/project"); got != `D:\shared\project` {
		t.Errorf("native path = %q, want D:\\shared\\project", got)
	}
	if got := mounts.nativePath("/drives/work-other/project"); got != "" {
		t.Errorf("native path outside mount = %q, want empty", got)
	}
}

func TestWSLDiscoversWindowsSources(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires the WSL discovery branch")
	}
	store, home := isolatedStore(t)
	t.Setenv("HOME", home)
	t.Setenv("WSL_DISTRO_NAME", "Fixture-WSL")
	bin, drive := t.TempDir(), t.TempDir()
	windowsHome := filepath.Join(drive, "Users", "示例")
	t.Setenv("PATH", bin)
	t.Setenv("KNOOO_TEST_WINDOWS_HOME", windowsHome)
	t.Setenv("KNOOO_TEST_WSL_LAUNCH", filepath.Join(bin, "unexpected-launch"))
	writeCommand := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeCommand("powershell.exe", `
case "$*" in *'[Console]::OutputEncoding'*'UTF8Encoding'*) ;; *) exit 1 ;; esac
[ -z "$KNOOO_TEST_INTEROP_UNAVAILABLE" ] || exit 1
printf '%s\n' '{"UserProfile":"C:\\Users\\示例","CODEX_HOME":"C:\\overrides\\codex"}'`)
	writeCommand("wslpath", `
[ "$1" = "-u" ] && [ "$2" = 'C:\Users\示例' ] || exit 1
printf '%s\n' "$KNOOO_TEST_WINDOWS_HOME"`)
	writeCommand("wsl.exe", `
case "$*" in
  '--list --quiet') printf '%s\n' Fixture-WSL Running-Sibling Stopped-Sibling ;;
  '--list --quiet --running') printf '%s\n' Fixture-WSL Running-Sibling ;;
  *) : >"$KNOOO_TEST_WSL_LAUNCH"; exit 1 ;;
esac`)

	nativeRoot := filepath.Join(home, ".codex", "sessions")
	windowsRoot := filepath.Join(windowsHome, ".codex", "sessions")
	overrideRoot := filepath.Join(drive, "overrides", "codex", "sessions")
	for _, root := range []string{nativeRoot, windowsRoot, overrideRoot} {
		putFile(t, filepath.Join(root, "rollout-fixture.jsonl"), fixture(t, "codex", home))
	}
	mounts := windowsMounts{{local: drive, native: "C:"}}
	sources, warnings, report := store.discover(map[string][]string{"codex": {`C:\overrides\codex\sessions`}}, mounts)
	if len(warnings) != 0 || len(sources) != 3 || len(report.Environments) != 4 {
		t.Fatalf("found %d sources, %d warnings, %d environments", len(sources), len(warnings), len(report.Environments))
	}
	for _, source := range sources {
		root := report.Roots[source.root]
		platform, distro := "windows", ""
		if root.Directory == nativeRoot {
			platform, distro = "wsl", "Fixture-WSL"
		}
		if root.Platform != platform || root.WSL != distro || root.State != "scanned" {
			t.Errorf("unexpected root attribution: %+v", root)
		}
	}
	for _, expected := range []Environment{
		{Platform: "wsl", WSL: "Fixture-WSL", Home: home, State: "scanned"},
		{Platform: "windows", Home: windowsHome, State: "scanned"},
		{Platform: "wsl", WSL: "Running-Sibling", State: "unavailable"},
		{Platform: "wsl", WSL: "Stopped-Sibling", State: "stopped"},
	} {
		found := false
		for _, environment := range report.Environments {
			found = found || environment == expected
		}
		if !found {
			t.Errorf("missing environment: %+v", expected)
		}
	}
	if _, err := os.Stat(os.Getenv("KNOOO_TEST_WSL_LAUNCH")); !os.IsNotExist(err) {
		t.Fatalf("unexpected sibling launch or stat error: %v", err)
	}

	t.Setenv("KNOOO_TEST_INTEROP_UNAVAILABLE", "1")
	sources, warnings, report = store.discover(nil, mounts)
	if len(sources) != 1 || len(warnings) != 1 || report.Environments[1].State != "unavailable" {
		t.Fatalf("partial discovery returned %d sources, %d warnings, environments %+v", len(sources), len(warnings), report.Environments)
	}
}
