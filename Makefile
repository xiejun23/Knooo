.PHONY: help dev ui-install ui-build build test vet package-win clean

VERSION := $(shell cat VERSION)

help: ## 显示常用工程命令
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

ui-install: ## 安装前端依赖
	npm --prefix ui ci --registry=https://registry.npmjs.org --replace-registry-host=always

ui-build: ## 校验 TypeScript 并构建前端到 ui/dist
	npm --prefix ui run build

dev: ui-build ## 构建前端并以开发模式运行 Knooo
	go run ./cmd/knooo

build: ui-build ## 构建当前平台可执行文件到 build/knooo
	@mkdir -p build
	go build -trimpath -o build/knooo ./cmd/knooo

vet: ## 运行 Go 静态检查
	go vet ./...

test: ui-build vet ## 运行前端类型检查与全部 Go 测试
	go test -count=1 ./...

package-win: ui-build ## 交叉编译 Windows x64 便携版与 NSIS 安装包
	@mkdir -p build
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -buildvcs=false \
		-ldflags "-H=windowsgui" -o build/Knooo-windows-x64.exe ./cmd/knooo
	makensis -WX -DAPP_VERSION="$(VERSION)" packaging/windows.nsi
	cd build && sha256sum Knooo-windows-x64.exe Knooo-Setup-windows-x64.exe > SHA256SUMS.txt

clean: ## 清理本地构建产物
	rm -rf build ui/dist
