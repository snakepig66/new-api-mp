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

---

## 阶段 1 · 数据与钱（2026-10-24 完成）

开发方式：主智能体监工、子智能体并行实现（接线 / 纯函数测试 / 数据库测试三路），主智能体验收、整改并复跑全部门禁。

做了什么：
- `money.go`：`Micro` 微元类型；`mulDivCeil` 用 128 位中间运算实现买家向上取整（§3.2），溢出/负数硬报错；`TokenCostMicro`、`ToolCostMicro`；`SplitPlatformFee` 实现 7% 四舍五入分成且 fee+credit==debit 恒等。
- `models.go`：§4.2 全部 11 张 `mp_` 表（sellers/price_books/per_call_prices/holidays/listings/probes/key_prefs/key_models/requests/ledger/payouts），金额列全整数微元，`mp_ledger` 有 `(request_id, entry_type)` 唯一索引。
- `migrate.go`：`Migrate` 三方言 AutoMigrate + users 自增水位 10011（MySQL `ALTER TABLE AUTO_INCREMENT`、PG `setval(..., false)`、SQLite `sqlite_sequence` UPDATE-or-INSERT）。
- `identity.go`：匿名买家代号 = `HMAC-SHA256(secret, seller|buyer)` 截 8 位 hex；`MP_KEY_SECRET` 缺失时报 `ErrNoAnonSecret`，错误不含密钥。
- `ledger.go`：复式流水。结算行集 = buyer_debit + tool_debit（仅按次费时）+ seller_credit(pending, T+7) + platform_fee，signed 求和为 0；`PostSettlement` 同 request_id 同数值重放幂等、不同数值报 `ErrSettlementConflict`，唯一索引兜底并发；`PromoteAvailable` 到期转正；`Reverse` 冻结期冲正（seller_credit 翻 reversed + 追加 refund/reversal/fee_reversal，三账户归零，active 行仍和为 0）；`AccountBalance` 按状态汇总。
- `marketplace.go`：`Init` 接线——启用时强制 `MP_KEY_SECRET`、设置匿名代号密钥、跑迁移；未启用时零影响。
- 测试：money/identity 纯函数表驱动测试；ledger 10 个用例（恒等式、无 tool、零额、幂等重放、冲突、20 goroutine 并发收敛、T+7 边界、冲正 happy/guards、空账户）；migrate 4 个用例（11 表、唯一索引、AUTOINCREMENT 探针、首用户 10011/次 10012、已有低 id 用户后新用户跳到 10011、三次迁移幂等）。

取舍与整改记录：
- 子智能体验收时发现 `sqlite_sequence` 无唯一索引且 sqlite 禁止对其建索引，原 upsert 写法在 prepare 期必败；主智能体已整改为 UPDATE-不存在再 INSERT，并删除测试 fixture 里的哨兵绕过。
- 冲正方案：原 seller_credit 翻 reversed（不再成熟），追加 refund(buyer,+total,available)、reversal(seller,−credit,reversed 配对抵消)、fee_reversal(platform,−fee,available)；全部行（含 reversed）与 active 行各自求和都为 0，满足 §3.9 恒等式与 `(request_id, entry_type)` 唯一约束（fee_reversal 属 §4.2 类型列表「…」的扩展）。
- 内存 sqlite 测试库连接池限 1（`cache=shared` 下 busy_timeout 对表锁无效）；并发用例仍过 -race。
- §10 待定项暂按文档默认推进：T+7、流式截断不收费、改倍率不回验真、买家自加模型按 §3.6；402/403 暂定 402；供应商白名单首版只接 DeepSeek（种子在阶段 2 配）。待产品负责人最终确认。

测试结果：
- `go build ./...` ✅
- `go vet ./marketplace/...` ✅
- `CGO_ENABLED=1 go test ./marketplace/... -race -count=1` ✅（25 个顶层测试全 PASS，无 DATA RACE）
- `gofmt -l marketplace/` 无输出 ✅
- MySQL/PostgreSQL 方言迁移留到阶段 7 docker 验证（AGENTS.md 数据库规则）。
