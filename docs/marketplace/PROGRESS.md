# Marketplace 开发进度

需求唯一来源：`docs/marketplace/MARKETPLACE-DEV.md`

- 上游基线：`33c61a24234aa20cb10e8a644304322c476361de`（QuantumNous/new-api main，2026-10-23）
- 工作分支：`mp`（不 push 到上游）

---

## 阶段 0 · 起步（2026-10-23 完成）

做了什么：
- clone 上游最新 main，记录基线 commit，建分支 `mp`。
- 建 `docs/marketplace/`，放入需求文档与本进度文件。
- 新建 `marketplace/` 包骨架（`marketplace.go`）：`Enabled()` 读 `MP_ENABLED`，`Init(db)` 为后续迁移/任务留口，未启用时直接返回。
- `main.go` 接缝（`// MP-HOOK`，2 处共 6 行）：import + `InitResources()` 成功后调用 `marketplace.Init(model.DB)`。

取舍：
- `Init` 现在接收 `*gorm.DB` 但暂不使用，避免阶段 1 再改函数签名。
- 未在本机长期运行服务（按产品负责人要求，运行验证放到 llmjar 服务器）。
- 本机为 Windows，`-race` 需要 CGO：已装 Go 1.27.0 与 WinLibs MinGW-w64 gcc 16.2.0（gcc 已入用户 PATH）。

测试结果：
- `go build ./...` 通过。
- `go vet ./marketplace/...` 通过。
- `go test ./marketplace/... -race -count=1` 通过（暂无测试文件）。
- `MP_ENABLED` 未设置时启动路径与上游一致（Init 直接返回）。
