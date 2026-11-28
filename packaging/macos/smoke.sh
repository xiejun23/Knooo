#!/usr/bin/env bash
set -euo pipefail
trap 'echo "macOS smoke check failed at line $LINENO" >&2' ERR

cd "$(dirname "$0")/../.."
dmg="$PWD/build/Knooo-macOS-universal.dmg"
tmp=$(mktemp -d)
mount="$tmp/volume"
pid=""
cleanup() {
  if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; fi
  if mount | grep -Fq " on $mount "; then hdiutil detach "$mount"; fi
  rm -rf "$tmp"
}
trap cleanup EXIT
hdiutil attach -readonly -nobrowse -mountpoint "$mount" "$dmg"
test "$(readlink "$mount/Applications")" = /Applications
app="$tmp/Installed Apps/Knooo.app"
mkdir -p "$(dirname "$app")"
ditto "$mount/Knooo.app" "$app"
hdiutil detach "$mount"
codesign --verify --strict --all-architectures "$app"
exe="$app/Contents/MacOS/Knooo"
lipo "$exe" -verify_arch arm64 x86_64

fixture="$tmp/Test User"
mkdir -p "$fixture/.codex/sessions" "$fixture/.claude/projects/example"
cp internal/sessions/testdata/codex.jsonl "$fixture/.codex/sessions/rollout-fixture.jsonl"
cp internal/sessions/testdata/claude.jsonl "$fixture/.claude/projects/example/thread.jsonl"
# Launch the installed GUI executable in an isolated home. Keep its capability out of CI logs.
env HOME="$fixture" "$exe" -listen 127.0.0.1:0 > "$tmp/launch" &
pid=$!
for ((i=0; i<120; i++)); do
  kill -0 "$pid"
  if grep -q '^http://127\.0\.0\.1:' "$tmp/launch"; then break; fi
  sleep 0.5
done
read -r launch < "$tmp/launch"
origin=${launch%/#open=*}
token=${launch#*#open=}
api() { curl --fail --silent --show-error -H "Authorization: Bearer $token" "$origin/api/$1"; }
curl --fail --silent "$origin/" | grep -q 'id="root"'
api about | jq -e --arg v "$(cat VERSION)" '.version == $v' >/dev/null
api sessions | jq -e '
  ([.sessions[] | select(.platform == "darwin") | .id] | sort) ==
  ["claude:claude-fixture", "codex:codex-fixture"]' >/dev/null
api 'session?id=codex:codex-fixture' | jq -e 'any(.session.messages[]; .text == "The sample builds.")' >/dev/null
api 'session?id=claude:claude-fixture' | jq -e 'any(.session.messages[]; .text == "Describe the sample.")' >/dev/null
api 'search?q=sample&page=1' | jq -e '.total == 2' >/dev/null
api backup | jq -e '.version == 1' >/dev/null
test -d "$fixture/Library/Application Support/Knooo"

# Check the installed process owns a visible normal window, then exercise the macOS Quit event.
swift - "$pid" <<'SWIFT'
import Cocoa
import CoreGraphics
let pid = pid_t(CommandLine.arguments[1])!
let deadline = Date().addingTimeInterval(30)
while true {
    let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
    if windows.contains(where: { ($0[kCGWindowOwnerPID as String] as? Int32) == pid && ($0[kCGWindowLayer as String] as? Int) == 0 }) { break }
    guard Date() < deadline else { fatalError("Knooo did not show a window") }
    Thread.sleep(forTimeInterval: 0.25)
}
guard NSRunningApplication(processIdentifier: pid)?.terminate() == true else { fatalError("Quit failed") }
SWIFT
for ((i=0; i<120; i++)); do
  if ! kill -0 "$pid" 2>/dev/null; then break; fi
  sleep 0.25
done
if kill -0 "$pid" 2>/dev/null; then echo "Knooo did not quit" >&2; exit 1; fi
wait "$pid"
pid=""
