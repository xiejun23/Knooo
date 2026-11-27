## 改动说明

<!-- 简明描述本次改动解决的问题或新增的能力 -->

## 关联 Issue

<!-- 例如 Closes #123（如无可留空） -->

## 验证清单

- [ ] 已执行 `npm --prefix ui run build`（前端类型检查与构建通过）
- [ ] 已执行 `go vet ./...` 与 `go test -count=1 ./...`
- [ ] 涉及 Windows 桌面壳时已验证 `GOOS=windows GOARCH=amd64 go build ./cmd/knooo`
- [ ] 新增文件已严格按 `AGENTS.md` 归位到对应目录，未在根目录散落文件
