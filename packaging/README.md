# Knooo Windows x64

| GitHub Release 文件 | 用途 |
| --- | --- |
| `Knooo-Setup-windows-x64.exe` | 为当前 Windows 用户安装，默认目录为 `%LOCALAPPDATA%\Programs\Knooo`，也可自选目录。添加开始菜单快捷方式和卸载入口。 |
| `Knooo-windows-x64.exe` | 直接双击运行的便携版。 |
| `SHA256SUMS.txt` | 两个 EXE 的 SHA-256。Windows PowerShell 可用 `Get-FileHash .\Knooo-windows-x64.exe` 核对。 |

安装版和便携版使用同一个 EXE，内嵌前端资源、Go 服务和本地数据库引擎。运行需要 Windows x64 和 Microsoft Edge WebView2 Runtime。检测到运行时缺失时，安装包从微软下载并验证签名后安装。便携版用户可从 [Microsoft 下载 WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/#download-section)。

从开始菜单启动会打开 Knooo 窗口。关闭窗口后，应用留在系统托盘，工作回顾继续运行。双击托盘图标可恢复窗口，右键选择「退出 Knooo」可关闭应用。安装版可在「本机设置」开启「登录 Windows 时启动 Knooo」，默认关闭。开启后，Knooo 在 Windows 登录时进入托盘，首次打开窗口时加载 WebView2。

应用默认把数据保存在 `%APPDATA%\Knooo`。卸载前请从系统托盘退出 Knooo。卸载移除程序、快捷方式和登录启动项，保留数据目录。

覆盖安装 v0.1.0 时，沿用原安装目录。安装器会将 `Knooo-Studio.exe` 替换为 `Knooo.exe`，并迁移已有的登录启动项。

应用按内置配置监听 `127.0.0.1:48763`，端口被占用时自动使用空闲的本机端口。从源码调试时可用 `go run . -no-open` 在终端查看地址。Linux 开发预览使用 `xdg-open`，WSL 优先调用 Windows 默认浏览器。

## 本地构建

在 Ubuntu 24.04 或 WSL 的仓库根目录运行以下命令。CI 使用 Node 22.23.1、Go 1.27.4 和 Ubuntu 的 NSIS `3.09-4ubuntu1`。

```bash
sudo apt-get update
sudo apt-get install -y nsis=3.09-4ubuntu1 nsis-common=3.09-4ubuntu1
npm ci --registry=https://registry.npmjs.org --replace-registry-host=always
npm run build
go test ./...

version=$(cat VERSION)
mkdir -p build
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -buildvcs=false \
  -ldflags "-H=windowsgui" -o build/Knooo-windows-x64.exe .
makensis -WX -DAPP_VERSION="$version" packaging/windows.nsi
(cd build && sha256sum Knooo-windows-x64.exe Knooo-Setup-windows-x64.exe > SHA256SUMS.txt)
```

`VERSION` 提供应用和 Windows 安装包的版本号。`.github/workflows/release.yml` 在推送与 `VERSION` 匹配的版本标签时构建并发布这三个文件。标签需加 `v` 前缀，例如 `v0.1.3`。`v0.1.3-rc.1` 一类的标签会发布为预发行版本。

## 准备版本日志

在本地让 Agent 执行「为 Knooo 准备版本 `vX.Y.Z`」。发版人仍然决定何时推送标签。

1. 用 `git fetch origin --tags` 拉取版本标签。用 `git tag --merged HEAD --sort=-version:refname` 查看当前分支可达的标签。正式版比较上一个已发布的正式版本。预发行版可以比较上一个已发布的候选版本或正式版。通过 GitHub Releases 核对基线。
2. 用 `git log <上一版>..HEAD`、`git diff --stat <上一版>` 和 `git diff <上一版>` 检查提交与本地待发布改动。首发还没有标签时，以待发布源码和测试结果为依据。
3. 将新版本号写入 `VERSION`，例如 `0.1.3`。在 `CHANGELOG.md` 顶部加入 `## vX.Y.Z · YYYY-MM-DD`，用 `- [更新] **简明标题**：说明内容。` 格式写清用户能感知的变化（类型支持 `更新`、`优化`、`公告`、`下线`，必要时可用 `修复`）。同一版本跨越多个日期时，可在版本下用 `### YYYY-MM-DD` 分组。有迁移步骤时把操作写进条目。保留已发布版本的日志。
4. 运行 `bash packaging/release-notes.sh vX.Y.Z`，确认输出的是本次更新内容。提交日志和待发布改动后，在需要发版时把标签打到该提交并推送。CI 用同一节生成 GitHub Release 正文。

## 应用内更新

Knooo 启动时检查 GitHub 上的正式版，使用中每小时检查一次。「关于」页也可手动检查。发现新版后，Windows x64 安装版可从右上角下载并安装。Knooo 比对 GitHub 提供的 SHA-256，退出后由安装器写入原安装目录，再打开新版。程序数据保存在 `%APPDATA%\Knooo`。便携版和开发预览提供 GitHub 下载入口。

应用使用匿名 GitHub 请求检查更新。面向所有用户发布时，仓库和正式 Release 需要公开可访问。

更换应用图标时，同步更新 `ui/assets/eagle.png` 和 `packaging/eagle.ico`，然后在 `packaging/` 目录重新生成 Windows 图标资源。

```bash
x86_64-w64-mingw32-windres eagle.rc -O coff -o ../eagle_windows_amd64.syso
```

也可用 Windows SDK 的 `rc.exe /fo eagle.res eagle.rc` 和 `cvtres.exe /machine:x64 /out:eagle.obj eagle.res`。随后在 WSL 生成 Go 链接器可用的资源文件。

```bash
objcopy --strip-debug --strip-symbol='@comp.id' --strip-symbol='@feat.00' eagle.obj ../eagle_windows_amd64.syso
```
