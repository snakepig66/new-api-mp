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

---

## 阶段 2 · 价目表与统一算价（2026-10-24 完成）

开发方式：子智能体 D 实现、子智能体 E 按文档验收清单独立写测试，主智能体复跑门禁并逐行审查算价代码。

做了什么：
- `timeslot.go`：`Calendar`（Asia/Shanghai，`time/tzdata` 内嵌兜底）；工作日 [09:00,12:00)∪[14:00,18:00) 半开区间判峰；节假日全天空闲（优先于周末）；调休上班日按工作日；`atomic.Pointer` 换日历保证 Reload 与 TierAt 无竞态；`ImportHolidays` 按日期 upsert（三方言 clause.OnConflict）。
- `pricebook.go`：`PriceAt`（effective_at <= at 中 effective_at DESC、version DESC 取首行；非 flat 未命中回退 flat；ErrNoPrice）；`PerCallPriceAt` 同规则并返回版本号。
- `quote.go`：唯一算价入口 `Quote`（§3.7 展示价/扣费/记录共用）。TokenDebit = ceil(Σtokens·P·mult/1e9)、ToolDebit = ceil(Σcount·U·mult/1000)，math/big 精确乘除、每个组成部分只向上取整一次；OfficialCost = 同 usage 按 mult=1000 的全价；工具次数 >0 无价目报 ErrNoPrice（§3.4 剥离前置条件）；负数与倍率越界（1..1000）报错；总额 int64 溢出检查。
- `seed.go`：DeepSeek 种子——deepseek-flash 空闲 1/0.02/4、高峰 2/0.04/8，deepseek-v4-pro 空闲 4.5/0.15/13.5、高峰 9/0.30/27（元/M，×1e6 转微元；缓存写=标准输入价，因 DeepSeek 无独立缓存写计费）；search/fetch 各 5000 微元/次，2026-11-01 00:00 +08:00 生效；token 价目 2026-01-01 生效；按唯一键查重幂等。价格来源 https://api-docs.deepseek.com/zh-cn/quick_start/pricing（2026-10-23 抓取，页面恰有这两个模型名，无需映射）。
- `marketplace.go` Init 在 Migrate 后调用 SeedPrices。

取舍：
- 规格中结构体名 Quote 与函数 Quote 冲突，结构体命名 QuoteResult（函数名与文档 §4.1 一致）。
- 节假日「年度导入接口」本阶段交付包级函数 ImportHolidays；HTTP 后台接口随阶段 6 接入。
- 下次切换时间（模型广场需要）留到阶段 5 再加，避免提前打开本文件。

测试结果（主智能体复跑确认）：
- `go build ./...` ✅ `go vet ./marketplace/...` ✅ `gofmt -l marketplace/` 无输出 ✅
- `CGO_ENABLED=1 go test ./marketplace/... -race -count=1` ✅（阶段 1 全绿 + 新增 24 个测试全绿，无 DATA RACE）
- 验收清单逐条落实：全部边界秒（11:59:59/12:00:00、13:59:59/14:00:00、17:59:59/18:00:00、09:00、08:59:59）、午休、周末、法定节假日、调休上班周末、跨档请求按冻结时刻整条同档、价目版本切换（effective_at 优先、同刻 version 大者优先、恰好生效时刻）、flat 回退、工具价目 2026-11-01 门控、一次取整、OfficialCost 基准、负数/倍率越界。

---

## 阶段 3 · 分享者与挂单（2026-10-24 完成）

开发方式：三个子智能体并行（egress / 挂单主体 / 验真），主智能体补 Init 出网接线、逐行审查安全关键代码、复跑门禁。

做了什么：
- `egress.go`（§3.11）：fail-closed 出网白名单（空表全拒；精确或点边界后缀匹配；IP 字面量永远拒绝）；`ValidateEgressIP` 拒绝 unspecified/loopback/private/link-local/multicast/CGNAT/benchmarking，先 Unmap 处理 IPv4-mapped；`EgressHTTPClient` 自定义 DialContext——每次拨号独立「白名单检查 → 解析一次 → 全部 IP 校验（任一不过整体拒）→ 用验证过的 IP 直接拨号」，无二次解析（DNS rebinding 的 TOCTOU 窗口不存在）；不走代理（防绕过）；策略拒绝与传输失败的错误语义分离。
- `vendors.go`：供应商白名单（首版 DeepSeek，base_url 平台固定，模型与种子一致）；`VendorBaseURLHosts` 供出网白名单派生。
- `keycrypto.go`：AES-256-GCM 加密（随机 nonce），HMAC-SHA256 指纹；两个子密钥经 SHA-256 域分离派生自 MP_KEY_SECRET（与匿名代号共用一份运维备份）；错误不含明文/密文/完整指纹。
- `listing.go`：挂单 CRUD 与状态机（draft→verifying→canary→live⇄paused→banned，banned 终态）；CreateListing 单事务内指纹查重 + 建上游渠道行（禁用态、占位 Key 非真 key）+ 建挂单；唯一索引冲突三方言映射 ErrDuplicateKey 兜底并发；UpdateMultiplier 10 分钟间隔、Version+1、不重验真；ReplaceKey 重新验真（→verifying）且 banned 拒换；ValidateHours 严格 HH:MM；所有写路径错误不含 key。
- `verify.go`：验真三件套（/models 有效性与延迟、/user/balance 余额 big.Rat 精确转微元、chat/completions 最小请求探测 prompt 缓存字段）；独立限流桶 VerifyLimiter（手写令牌桶 + 并发信号量，go.mod 无 x/time 未加依赖）；VerifyListing 编排（draft→verifying→canary，失败回 draft 并写 mp_probes 探针，Detail 不含 key）；上游错误只记语义不回显 body（§3.11）。
- `marketplace.go`：Init 接线出网白名单——MP_EGRESS_DOMAINS 设置时覆盖，否则从供应商白名单 base_url 派生。

取舍：
- 验真失败回退边（verifying→draft）在 verify.go 内以条件更新实现并注释，listing.go 的公开状态机不暴露该边。
- 渠道行 Key 字段存占位符（mp-managed:<fp 前 8 位>），真 key 只在 mp_listings.key_ciphertext（加密）——满足 §3.5「任何接口不返回 key」；阶段 4 转发时按需解密注入。

测试结果（主智能体复跑确认）：
- `go build ./...` ✅ `go vet ./marketplace/...` ✅ `gofmt -l marketplace/` 无输出 ✅
- `CGO_ENABLED=1 go test ./marketplace/... -race -count=1` ✅（累计 70+ 测试全绿，无 DATA RACE）
- 验收清单：SSRF/DNS rebinding 用例（好坏 IP 混合整体拒、拨号 IP 即校验 IP、每次拨号独立校验）、重复 key 拒绝（同 seller 与跨 seller）、状态流转合法/非法边、改倍率 10 分钟、验真限流桶隔离（并发信号量 + 速率令牌）、错误/探针不含 key。
