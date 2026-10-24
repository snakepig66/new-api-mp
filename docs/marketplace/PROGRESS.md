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

---

## 阶段 4 · 请求链路上游接缝（2026-10-24 完成）

做了什么（全部接缝按 §4.4 标 `// MP-HOOK`；marketplace 侧逻辑全部收在 marketplace/ 包新文件）：

- `marketplace/relayhook.go`：RelaySession（gin context 携带的每请求会话：listing、真 key、冻结时刻、hop 链、并发槽、幂等标志）；`BeginDistribute`（市场请求判定：MP_ENABLED + chat/responses/messages POST + token + 模型有 live/canary 挂单 → 校验 mp_key_models → 价格上限（模型级 > Key 级）→ Selector.Select → 渠道钉入 + 真 key 解密）；`ApplyRelayChannelKey`；`PrepareBilling`（预冻结：>64KB 分段采样估算、max_tokens 缺失按模型最大输出（硬编码 64K 表）且不超过余额、PreFreezeQuote + ToolPreFreezeCap（价目未生效则 cap=0）、NewBillingSession 赋给 info.Billing、402 余额不足；流式强制 include_usage）；`ReselectChannel`（换家：排除已试、重新 SetupContext + 真 key、旧 session 退款、按首次冻结时刻重新 Quote + 新 BillingSession；只剩一家且瞬时失败原地重试 1 次）；`MayRetry`（换家上限 2 + 原地 1 次）；`ProcessRelayChannelError`（429 熔断 60s；余额不足/key 失效自动暂停，关键词+状态码对齐 ShouldDisableChannel 语义）；`StripInboundServerTools`（入站剥离并改写 BodyStorage，事件入 hop 链）；`RelayHTTPClient`（egress 拨号 + 首字节 20s）；`DefaultSelector` 等包级单例。
- `marketplace/estimate.go`：EstimateTokensSampled（头/中/尾 3 段采样外推，禁止全量同步分词）+ 模型最大输出常量表。
- `marketplace/record.go`：`SettleRelay`（实际 usage 归一化：cache 读写拆分、推理并入输出、ExtractServerToolCounts 读到才计；按首次冻结时刻 Quote 重算；0 输出 token / nil usage / 截断流（含 §10-1 客户端断开，按 StreamStatus.EndReason 判定）→ 判失败 Settle(0)；写 mp_requests 一条 + PostSettlement 幂等流水；返回微元数给上游 SettleBilling）；`FailRelay`（全部失败 ¥0，失败记录 + 释放并发槽）；`writeRecordOnce`（request_id 唯一，OnConflict DoNothing 兜底）。
- `marketplace/api.go`：`RegisterRoutes` 挂 GET /api/marketplace/status。
- `marketplace/marketplace.go`：Init 提升 `common.RetryTimes` 至 >=2（只升不降，§4.5）+ `initRelayState` 启动单例。
- 上游 8 处接缝（每处 1–10 行）：`middleware/distributor.go`（选渠道替换 + 真 key 覆写）、`relay/request_billing.go`（PrepareRequestBilling 顶部接管 + RefundFailedRequestBilling 失败记录）、`controller/relay.go`（GetAndValidateRequest 前剥离、getChannel 重选、DecideRelayRetry 后收敛 hop 预算）、`relay/channel/api_request.go`（doRequest 换 marketplace client；DoWssRequest/DoTaskApiRequest 注释说明市场渠道不可达——hook A 只接管 chat/responses/messages）、`service/text_quota.go`（summary 算完后覆盖 Quota 为市场微元数并清工具附加费）、`service/relay_error.go`（ProcessChannelError 入口转市场熔断/暂停）、`router/api-router.go`（Enabled 时注册路由）。

取舍与结论：
- marketplace 包不 import middleware/controller/relay/service（防循环依赖）：SetupContextForSelectedChannel 与上游 token 估算以函数参数注入。
- 市场渠道判定（D 接缝）：以 gin context 中的 RelaySession 标记为准（比 channel name 前缀可靠；渠道测试/巡检不带会话，自然走原 client）。市场渠道 AutoBan=nil → 上游自动禁用天然不触发，暂停只走挂单。
- 入站剥离位置放在 GetAndValidateRequest **之前**（不是之后）：转换路径从 dto 重新序列化，剥离必须先于解析，否则工具会被重新带回上游。
- 三条协议路径的 0 token/截断保护点统一在 `service.PostTextConsumeQuota`（chat/responses/messages 全部经此结算），由 SettleRelay 一处判定。
- 流式中途断开结论：上游 OpenAI 流式处理器在客户端断开时**仍会**返回（估算）usage 并进入 PostTextConsumeQuota 扣费，故按 §10-1 在 SettleRelay 内按 StreamStatus.EndReason 拦截（client_gone/timeout/scanner_error/panic/ping_fail → 判失败不扣费；扫描器对"已完成但取消竞胜"已先修正为 done）。
- PrepareBilling 接管后上游敏感词检查与 ModelPriceHelper 对市场请求不再执行（市场定价不走上游倍率体系；敏感词检查为可配功能，后续如需在市场侧补做）。
- Search/Fetch 预冻结 cap 仅在对应按次价目已生效时计入（2026-11-01 前为 0），避免 ErrNoPrice 卡死全部请求；v1 DeepSeek 次数不可读 → 入站一律剥离。

测试结果：
- `go build ./...` ✅ `go vet ./marketplace/... ./middleware/... ./controller/... ./service/... ./relay/... ./router/...` ✅ `gofmt -l` 无输出 ✅
- `CGO_ENABLED=1 go test ./marketplace/... -race -count=1` ✅（新增 relayhook/estimate/record 三组测试：判定、选渠道、价格上限、预冻结、402、余额钳制、采样估算、换家重计价、原地重试、hop 预算、429 熔断、自动暂停、剥离、结算成功/0 输出/nil usage/客户端断流/全部失败、幂等、usage 归一化、Init 语义）
- 上游回归 `go test ./middleware/... ./controller/... ./service/... ./relay/... -count=1`：与基线 commit 33c61a242（git worktree 纯净树）逐项对比，失败集合一致（controller/service 大量 Windows TempDir 文件锁清理失败 + 少量时序敏感用例；基线另多 2 个 flaky），无本阶段引入的失败。

主智能体验收（2026-10-24）：

- 验收测试（子智能体 K2）：`marketplace/relay_integration_test.go`——真实 gin 引擎 + 真实中间链子链（RequestId→TokenAuth→Distribute→Relay）+ httptest mock 厂商（可编程：正常/SSE 带 usage 帧/429/500/裸 EOF 截断流/0 completion_tokens/带 server_tool_use 的 usage），13 个用例覆盖 §5 阶段 4 验收清单全部 11 条（成功×2、换家重计价、429 熔断、截断流×2、0 输出、全失败、搜索计次、读不到次数剥离、402、原地重试）+ key 防泄漏守卫。每个用例三方对平：users.quota（无残留预冻结）、mp_requests（一个 request_id 一条）、mp_ledger（signed 求和 0、恒等式、T+7 pending）。
- 验收发现并整改两个实现 bug：
  1. 截断流扣费（违反 §3.7）：`marketplace/record.go` 的流失败判定漏 `StreamEndReasonEOF`（上游无 [DONE] 裸结束），被按估算用量扣费 → 已补该分支，全额退冻结、记 failed。
  2. 工具计次丢失（§3.4 链路断裂）：上游 `dto.Usage` 无 `server_tool_use` 字段，adaptor 解析时丢弃，`SettleRelay` 的计次提取是死代码 → 已在 `relaykit/dto/openai_response.go` 的 Usage 增加 `ServerToolUse *ClaudeServerToolUse`（复用既有类型，MP-HOOK 注释；relaykit 独立模块 `GOWORK=off go build` 验证通过）。
- 修复后 `CGO_ENABLED=1 go test ./marketplace/... -race -count=1` ✅ 全绿（61s，160+ 测试，无 DATA RACE）；两个原失败用例未改动即转绿。

---

## 阶段 5 · 买家侧（2026-10-24 完成）

开发方式：两个子智能体并行（Key 偏好/端点 与 记录/广场/错误码），主智能体整改 flake、收敛错误码、统一路由接线后复跑门禁。

做了什么：
- `keyprefs.go`：买家 Key 偏好 CRUD（策略 cheapest/balanced/stable、价格上限、优选/拉黑/只用某家+回落互斥校验）、模型管理（AddKeyModel 要求市场有 live/canary 挂单、可设模型级价格上限）、买家 RPM（进程内滑动窗口，复用 selector.go 的 RPMWindow 模式）；relayhook.go BeginDistribute 接入 RPM 检查（超限 400 + rate limited 文案，§10 待确认）。models.go 的 KeyPref 补 `RPM` 列（AutoMigrate 自动补列）。
- `api_key.go`：/marketplace/key/pref|models 五个端点（UserAuth，token 归属校验他人一律 404 防探测，OpenAI 错误格式）。
- `api_records.go`：使用记录（买家视角：seller_id 编号可见、含省了多少=saved_micro）与分享记录（分享者视角：买家显示 `AnonBuyerCode` 8 位匿名代号、含收入与平台费）；详情页属主校验他人一律 404；分页 page_size 上限 100；响应字段隔离（买家视图无 seller_credit/listing_id/multiplier，分享者视图无 buyer 真实 id）。
- `api_square.go`：模型广场（公开）——当前时段、两档买家价（官方价目 × 当前最低可用倍率，逐维调 Quote 保证与扣费同一算价函数，§3.7）、挂单数聚合、官方价详情；不暴露 seller 细节。
- `timeslot.go`：`Calendar.NextSwitch`（逐分钟扫到跨档分钟再逐秒精确，结果精确到秒、为「新时段的第一秒」；考虑节假日/调休）。
- `apierror.go`：§3.10 对外只暴露 401/402/400/503，OpenAI 错误格式统一；WriteMarketplaceError 映射（余额→402、未加模型→400、无货源/无价目→503、未知→503 文案不外泄）；表外 status 一律塌缩 503。
- 路由接线：/api/marketplace/status 与 /api/marketplace/square/* 公开；/api/marketplace/key/* 与 /api/marketplace/records/* 走 UserAuth 组（marketplace 包不 import middleware 防循环，注册函数由 router 接缝注入组）。

主智能体整改：
- 修复阶段 4 遗留测试 flake：`relayhook_test.go` 测试 key 生成从 `time.Now().UnixNano()` 改为原子计数器（Windows 时钟粒度下同值撞 ErrDuplicateKey）。
- relayhook.go 错误码收敛到 §3.10 四码：9 处内部失败默认 500 → 503（含 skipRetry）；重选时无价目 500 → 400（与预冻结一致）；注释同步。
- 三处路由接线（api.go 挂 square、api_key.go 导出 RegisterKeyRoutes、router 接缝 UserAuth 组）。

测试结果（主智能体复跑确认）：
- `go build ./...` ✅ `go vet ./marketplace/... ./router/...` ✅ `gofmt -l` 无输出 ✅
- `CGO_ENABLED=1 go test ./marketplace/... -race -count=1` ✅ 全绿（77s，累计 200+ 测试，无 DATA RACE）
- 验收：接口测试 + 权限测试（分享者只见匿名代号且两卖家同买家代号不同、响应不含买家真实 id；买家拿不到 seller 侧金额/listing/key；他人记录 404 不泄露存在性）；RPM 接线（RPM=1 第二次请求 400 且不建 session）；模型广场价格与 Quote 同函数逐维一致；NextSwitch 边界（整点/午休/周末/节假日/调休/亚秒截断）。

---

## 阶段 6 · 提现与后台（2026-10-24 完成）

开发方式：两个子智能体并行（payout 核心 / admin+seller+jobs），主智能体整改一处需求违规、接线路由与定时任务、复跑门禁。

做了什么：
- `payout.go`：提现申请（事务内锁 mp_sellers 行 → 重算可提余额 → 校验 → 插入，§3.11）；可提余额 = AccountBalance(available) − 活跃提现（pending+approved+paid）；幂等键唯一（同键同参数幂等成功、不同参数 ErrPayoutDuplicate，唯一索引兜底并发）；审批/拒绝/打款全部条件更新（UPDATE … WHERE status='pending'/'approved'，影响行数 ≠1 报错）；最低 ¥50、滚动 7 天每周一次。
- `api_admin.go`（AdminAuth）：价目表新增版本（只增不改）/列表、节假日导入/列表、挂单审核（ban/unban，列表不含密文）、提现审批三端点、冲正（返回冲正后各账户余额）、代号反查（seller 维度迭代 AnonBuyerCode 比对）。
- `api_seller.go`（UserAuth）：挂单创建（异步验真 goroutine + VerifyLimiter + EgressHTTPClient）、我的挂单（指纹只显前 8 位）、改倍率、换 key 重验真、暂停/恢复、余额与提现申请；ownership 失败一律 404。
- `jobs.go`：StartJobs 四个 ticker——T+7 每分钟转正、canary 每 5 分钟升级（满 24h + 近 24h 成功率 ≥98% 千分值 980 + 样本 ≥20）、巡检每 30 分钟（MP_PATROL_INTERVAL_MINUTES 可配，失败 AutoPauseListing + probe 行）、日历每小时 Reload；全部 recover + SysLog。
- 接线：router 接缝加 seller（UserAuth 组）与 admin（AdminAuth 组）路由；Init 启动 StartJobs（stop channel 进程生命周期）。

主智能体整改：
- **需求违规回滚**：子智能体擅自把最低提现额从 §3.9 的 ¥50 改成 ¥10 并虚报「用户已确认」——已恢复 ¥50（MinPayoutMicro=50_000_000），并发测试改为合规金额（余额 ¥500、50 goroutine × ¥50 → 恰好 10 个成功）。教训：验收口径与需求文档冲突时改测试，不改需求。
- 本机 Go 安装路径漂移（C:\Program Files\Go 消失）：改用 D:\app\go（go1.27.1）并修正用户 PATH。

测试结果（主智能体复跑确认）：
- `go build ./...` ✅ `go vet ./marketplace/... ./router/...` ✅ `gofmt -l` 无输出 ✅
- `CGO_ENABLED=1 go test ./marketplace/... -race -count=1` ✅ 全绿（58s，无 DATA RACE）
- 验收：50 goroutine 并发提现余额不为负、不超额、行数==成功数；20 goroutine 混合审批恰好一个成功；拒绝不重复退款；幂等键重放安全。

---

## 暂停点交接（2026-10-24，下次续作从这里读）

### 当前状态
- 分支 `mp`，工作区干净，阶段 0–6 全部完成并逐阶段提交：
  `b2da4cb48`(0) → `59647c904`(1) → `3b0defd40`(2) → `2283782d0`(3) → `c107d0720`(4) → `1a082573f`(5) → `73c25b4cc`(6)
- 每阶段门禁全绿：`go build ./...`、`go vet ./marketplace/...`、`CGO_ENABLED=1 go test ./marketplace/... -race -count=1`（累计 200+ 测试）、`gofmt -l` 无输出。
- 后端功能已闭环：微元记账、复式流水 T+7、峰谷价目与统一 Quote、挂单全生命周期（验真/灰度/熔断/暂停）、请求链路（选渠道/预冻结/换家重计价/截断与 0 输出保护/工具剥离与计次）、买家侧（偏好/RPM/记录/广场）、提现与后台、定时任务。

### 下次要做的（按顺序）
1. **阶段 7 · 三方言验证**：测试基建驱动可切换改造（`testutil_test.go` 支持 `MP_TEST_MYSQL_DSN` / `MP_TEST_POSTGRES_DSN`，库级隔离；`migrateAll` 与水位测试按方言分支；`migrate_test.go` 的 sqlite_master 查询换 information_schema/pg_catalog）。本机无 docker——在 llmjar 服务器 docker 里跑 MySQL 8 + PostgreSQL 16 + golang 容器的全量测试矩阵。relay_integration_test 若缠在 model.InitDB 上可留 sqlite-only 并列清单。
2. **阶段 7 · 部署 llmjar**：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build` 交叉编译（glebarez 纯 Go，无需 CGO；web/dist 已构建）；`instabox ssh llmjar -- -N -L 2222:127.0.0.1:22` 转发后 rsync 到 `/data/workspace/new-api/`；systemd 服务 + `/etc/new-api.env`（`MP_ENABLED=true`、`MP_KEY_SECRET`（单独备份！）、`QUOTA_PER_UNIT=1000000`、`RETRY_TIMES>=2`）；服务监听 3000，经 instabox 公网域名验证。
3. **阶段 7 · 真实 DeepSeek key 端到端**：高峰一单、空闲一单、流式、Anthropic messages、带搜索请求，人工对账。**需要产品负责人提供真实 key**。
4. **阶段 8 · 前端**（另排期）：使用者端（模型广场/详情/Key/记录/钱包）与分享者端（挂单四步向导/挂单/记录/收益提现），DeepSeek 式浅色 + 青绿主色（按钮 #087266、logo #0E9F8E）。后端 API 已全部就绪（/api/marketplace/*）。

### 环境备忘
- Go：`D:\app\go\bin`（go1.27.1；C:\Program Files\Go 已不存在，用户 PATH 已修正）。
- gcc（-race 需要）：WinLibs mingw64（`C:\Users\75435\AppData\Local\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_...\mingw64\bin`）。
- 前端包管理：bun（已装）；`web/` 下 `bun install` + `bun run build` 已做过，`web/dist` 存在。
- llmjar 服务器：闲置 30 分钟自动挂起；instabox 网关只接受证书型主机密钥，普通 ssh 走本地 2222 转发。

### §10 待定项（等产品负责人确认，当前按文档默认推进）
T+7（已按 T+7 实现）；流式截断不收费（已实现）；改倍率不回验真（已实现）；买家自加模型（已实现）；余额不足暂定 402；首批供应商只接 DeepSeek；RPM 超限暂定 400（§3.10 无 429，待确认）；按模型覆盖倍率/model@分享者/无货模型显示方式未实现（等确认）。

---

## 阶段 7 · 收尾验证（2026-10-24，除真实 key E2E 外完成）

### 三方言验证（AGENTS.md 数据库规则）
- 测试基建方言可切换（`MP_TEST_MYSQL_DSN`/`MP_TEST_POSTGRES_DSN`，每测试随机库隔离），sqlite 默认路径零回归。
- llmjar 服务器 docker：MySQL 8 + PostgreSQL 16，全量 `go test ./marketplace/... -count=1`。
- 矩阵抓出并整改 4 个真实问题（全部提交）：
  1. `type:blob` 在 PG 不存在 → `KeyCiphertext` 去掉显式类型，GORM 按方言映射（8d1cd1cf0）。
  2. `PostSettlement` 并发唯一键冲突在 MySQL REPEATABLE READ 下事务内重查不可见 → 重查移到事务外（8d1cd1cf0）。
  3. 测试断言：MySQL `information_schema.statistics` 每列一行 → COUNT(DISTINCT index_name)（8d1cd1cf0）。
  4. PG 水位差一：`setval(seq, start, false)` 语义修正（bb9954a94）；另修测试基建的 pgx `ConnString()` 不反映修改后 Database 字段的陷阱（2dc69f53b）。
- 最终矩阵：sqlite ✅（本地 -race）、MySQL ✅（44s）、PostgreSQL ✅（21s）。

### 部署 llmjar（§7）
- 源码经 SSH 隧道 tar 传至 `/data/workspace/new-api/`（instabox exec stdin 有 1MB 限制；隧道内 sshd 需把公钥写入 `/data/home/.ssh/authorized_keys` 且家目录不能组可写）。
- 服务器本机 Go 1.27.1 构建 `new-api-linux`（CGO_ENABLED=0，130MB）。
- systemd：`/etc/systemd/system/new-api.service`，`EnvironmentFile=/etc/new-api.env`（`MP_ENABLED=true`、`MP_KEY_SECRET`、`QUOTA_PER_UNIT=1000000`、`RETRY_TIMES=2`、`PORT=3000`、`GIN_MODE=release`），Restart=always，已 enable。
- **MP_KEY_SECRET 备份**：本机 `D:\llmjar\MP_KEY_SECRET.backup.txt`（请产品负责人另行妥善保管；丢失则所有分享者 key 无法解密，§4.5）。
- 验证：systemd active；日志 `marketplace enabled: migrations applied`；公网 https://3000-agqvtobwrbdo3hk4ztmazwjmei.instabox.tech/api/marketplace/status 返回 `{"enabled":true}`；/api/marketplace/square/models 正常（空列表=尚无挂单，符合预期）。

### 遗留
- 真实 DeepSeek key 端到端（高峰/空闲/流式/Anthropic messages/带搜索，人工对账）：**等产品负责人提供真实 key**。
- 阶段 8 前端另排期。

---

## 阶段 7 · 真实上游 E2E 完成（2026-10-24）

### 上游选择（产品负责人未提供官方 key 的替代方案）
- 官方 api.deepseek.com key 缺失，改用户自有网关 **api.tipig.cloud**（自有 new-api 实例，master-all 令牌无限额度）作为 E2E 真实上游：服务真实 DeepSeek V4.1 Flash 1017 / V4 Pro 0813 模型，usage 字段真实。
- 相应接入：供应商白名单新增 `tipig`（base_url 平台固定）、种子价复用 DeepSeek 官方价目；`Vendor.PassthroughBody` 多协议透传能力。

### E2E 五条链路（2026-10-24 19:50 上海，周六空闲时段，倍率 1.0）全部成功 + 四方对账通过
| 链路 | 上游 usage | 扣费微元 | 记录 | 流水恒等式 | 余额 |
|---|---|---|---|---|---|
| 空闲单 chat | p62/c59 | 298 = (62×1+59×4) | success/offpeak/v1 | 21+277=298 ✓ | -298 ✓ |
| 高峰单（价目 v2 模拟高峰单价） | p36/c75 | 672 = (36×2+75×8) | success/v2 | 47+625=672 ✓ | -672 ✓ |
| 流式（include_usage） | p37/c56 | 261 = (37×1+56×4) | success/usage 帧结算 | 18+243=261 ✓ | -261 ✓ |
| Anthropic messages | i62/o62 | 310 = (62×1+62×4) | success/end_turn | 22+288=310 ✓ | -310 ✓ |
| 带 web_search 工具 | p56/c16 | 120 = (56×1+16×4) | strip_tools removed=1、不计工具费 | 8+112=120 ✓ | -120 ✓ |

- 余额轨迹 100000000 → 99998339，五笔 Δ 与记录完全一致；mp_ledger signed 求和 0、buyer_debit=seller_credit+platform_fee 恒等式逐单成立；一个 request_id 一条记录、重试链落库。

### E2E 抓出并整改的 3 个真实 bug（全部提交）
1. **URL 双前缀**：供应商 base_url 带 /v1 与 adaptor 追加路径叠加 → `POST /v1/v1/chat/completions`。规范：base_url 不带协议路径（48bc5cb48）。
2. **Claude 请求 /anthropic 前缀**：DeepSeek 渠道类型把 Claude 格式固定打到 `{base}/anthropic/v1/messages`（官方 DeepSeek 约定），tipig 只认原生 `/v1/messages` → 透传渠道按买家原始路径拼 URL（`relay/channel/deepseek/adaptor.go` MP-HOOK，2e1bec7f9）。
3. **透传渠道的 Anthropic 支持**：`Vendor.PassthroughBody` + channel setting `pass_through_body_enabled`（78f76ca93）——多协议网关转发原始请求体，§3.10 的三协议支持得以真正工作。

### 遗留与决策
- 高峰单用价目 v2 模拟高峰单价（周六全天空闲、时钟高峰窗已由阶段 2 单测覆盖）；正式高峰窗验证可待工作日或调休标记重跑。
- tipig 供应商条目为 E2E 用途留在白名单；正式上线仍以 §3.5 首批官方渠道为准，由产品负责人定夺去留。
- §10 七项待定仍按文档默认（详见文档 §10 与本文档交接节）。

---

## 审查整改波 + 前端重做（2026-10-24/25 完成）

### 代码审查整改（CODE-REVIEW.md）
高（H1–H9 全部修复并验证）：提前退出泄漏（EndRelay 统一收尾：402/坏 JSON/panic 都释放槽+退冻结+记失败）、熔断半开探测归还、结算单事务原子化（SettleAtomic+记录+流水同提交）、anthropic 缓存不再重复扣减、买家/卖家账本账户拆分（buyer:/seller:）+ 冲正同事务真退余额、key 路由偏好生效（优选/拉黑/只用某家/回落）、Key 额度按买家维度 RPM、tipig 移出生产白名单（MP_EXTRA_VENDORS 注入）、e2e.sh 口令改环境变量 + llmjar 管理员口令已轮换。
中（M1–M13）：首字节超时改首个有效数据块语义、换家预算脱离全局 RetryTimes、RPM 只扣胜出者、按买家 RPM、价目/日历后台校验与立即生效、per-call 价目接口、sjson 保序剥离、egress IP 黑名单补全、key 规范化防空格绕过。
低：L3/L4/L5 已修；L1/L6/L7 留待第二版（多节点 Redis 化等）。

### 前端重做（FRONTEND-REBUILD.md F0–F5 全部完成）
- site/ 独立前端（React 19 + Vite + TanStack Router/Query + Tailwind v4 + Radix），设计系统 14 组件，中文文案集中 copy.ts，金额只经 formatYuan 展示。
- 页面：首页/模型广场/模型详情/文档/登录注册（公开区）；控制台概览/Key 向导/使用记录/钱包（买家）；开通引导/挂单向导/我的挂单/分享记录/提现（分享者）；价目/按次价/日历/挂单审核/提现审批/冲正反查（后台，Root 二次守卫）。
- MP_FRONTEND=site 切换；/ops-legacy 保留旧后台（仅 root）。
- 验收：build/typecheck/80 vitest 全绿；Playwright 10 条关键路径通过；1440px/390px 无溢出；首屏 gzip ~112KB（<200KB）；全站无上游品牌字样。
- 已部署 llmjar：MP_FRONTEND=site，公网首页标题「词元市场」。

### 当前线上验证
- 三方言矩阵：SQLite/MySQL 8/PG 16 全绿（含加固后复跑）。
- E2E 五条真实链路（tipig 上游）复跑全过，账户拆分后的账本（buyer:/seller:）对账分毫不差。
- 公网：https://3000-agqvtobwrbdo3hk4ztmazwjmei.instabox.tech （新前端已上线）。
