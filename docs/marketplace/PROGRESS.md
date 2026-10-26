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

### 待定项清单（需产品负责人确认，当前按文档默认实现，均可低成本修改）
- §9-1 产品名/logo：占位「词元市场」+ 极简 SVG 文字标（#0E9F8E）。改名只需改 site/src/copy.ts 的 brand.name 一处。
- §9-2 无货模型：默认隐藏（广场不出行；详情页直达可访问并提示「暂时没人挂单」）。要改灰显改 features/market 的过滤一处。
- §9-3 旧后台：保留 /ops-legacy（仅超管）。要移除改 router 一处 + 关 MP_FRONTEND 切换。
- §10（MARKETPLACE-DEV）：T+7/流式截断不收费/改倍率不回验真/买家自加模型已按默认实现；402、只接 DeepSeek 官方、RPM 400、按模型覆盖倍率与 model@分享者 未实现待确认。

---

## 视觉改版（VISUAL-SPEC.md，DeepSeek 风格，2026-10-25 完成并验收）

### 实现
- §1 令牌整体替换 styles.css @theme：唯一强调蓝 #4d6bfe、近黑 CTA #0f1115、Inter、8/12/20/pill 圆角、无阴影；旧青绿（#0e9f8e/#087266/mint/teal）全站清零（源码与线上 grep 均零命中，favicon/logo.svg/theme-color 同步换蓝）。
- §0 先修：deploy.sh 顺序改为构建→上传→哈希校验→MP_FRONTEND=site→**重编译二进制**→重启（embed 读取新 dist）；index.html no-cache、assets immutable；路由 errorComponent 识别 chunk 加载失败并自动 reload 一次、中文错误页；占位文案（运营主体/ICP/示例徽标/版式示例/筛选将开放/实时数据接入后）全部下线。
- §4 逐页改法 V3（公开区）/V4（控制台）两波完成；类名清理 260+168 处。

### 部署关键修复
- deploy.sh 编译目标 `new-api` 而 systemd 运行 `new-api-linux`：重启后仍是旧二进制。改为 `go build -o new-api-linux`，部署后 systemctl 确认主进程为新编译产物。

### §5 验收（全部通过）
- 全站 grep：旧色/rounded-[4px]/text-[11/text-[12.5px] 零命中；等宽字体仅代码块。
- 浏览器实测（IAB，线上）：首页/模型广场/文档/登录 1440px 逐页截图核验（蓝强调、黑胶囊 CTA、hero 蓝白渐变、/docs 三栏+brand-soft 激活胶囊+深色代码块、登录分屏左深蓝渐变）；390px 移动端四页 scrollWidth==clientWidth 无横向溢出、无白屏、无占位文案（首页汉堡菜单/标题缩放正常）。
- /models/deepseek-flash 部署后整页导航（硬刷新）渲染正常，无 chunk 报错。
- 首页数据条按 §0 收紧：models_on_sale=0 时整条隐藏（不出「0 / —」占位观感），补 2 条单测，vitest 96/96 绿。

### 待定项更新
- §9-1 产品名仍为默认「词元市场」；logo 已按 VISUAL-SPEC 蓝点风格（待正式定名后出正式版）。

---

## GitHub 登录（2026-10-25）

- 前端与上游 OAuth 链路原本已实现（登录/注册页按钮 → `/api/oauth/state` 取 state → GitHub 授权 → SPA `/oauth/github` 回调换取会话），缺的是配置注入方式。
- 新增环境变量支持（`model/option.go: applyOAuthEnvOverrides`）：`GITHUB_CLIENT_ID`、`GITHUB_CLIENT_SECRET`、可选 `GITHUB_OAUTH_ENABLED`。两个凭据配齐即自动开启；显式 true 但缺凭据会拒绝并记错误日志；每次从数据库同步选项后重新应用，保证 env 优先且不被 SyncOptions 覆盖。Go 单测 6 例覆盖（`model/option_oauth_env_test.go`）。
- 隔离实例实测（/tmp 新库 + 3901 端口 + 假凭据）：`/api/status` 正确返回 `github_oauth=true` 与 client id；生产实例无凭据时保持 `false`，登录页按钮维持禁用说明。
- 回调地址必须注册为 `https://<本站域名>/oauth/github`（SPA 路由，已验证 200）。
- 配置步骤见 `docs/marketplace/GITHUB-LOGIN.md`；真正上线还差一次人工动作：在 GitHub 创建 OAuth App 并写入 `/etc/new-api.env` 后重启（凭据不在仓库、本机或日志中保存）。

---

## 全站统一货币：人民币（2026-10-25）

- SPA 本就全部经 `formatYuan` 输出 `¥`（market 金额均为微元整数），本次统一的是原生/旧控制台与底层额度换算：
  - 数据库选项 `general_setting.quota_display_type = CNY`（旧控制台显示 ¥ 而非 $）；
  - `USDExchangeRate = 1`，使「1,000,000 额度 = ¥1.00」与市场微元记账、SPA 展示完全一致；
  - 修复 `QUOTA_PER_UNIT` 环境变量从未被读取的问题：实际一直是默认 500000，旧控制台金额会是新站的两倍。现在启动时读取，运行时确认 `quota_per_unit=1000000`；
  - `DisplayInCurrencyEnabled` 旧布尔开关只区分「货币 / tokens」，不再把已选的人民币改回美元（单测覆盖）。
- 线上 `/api/status` 实测：`quota_display_type=CNY`、`display_in_currency=true`、`usd_exchange_rate=1`、`quota_per_unit=1000000`。
- 注意：汇率改为 1 后，旧控制台里按 USD 计价的渠道余额会按面值显示；本站上游（DeepSeek/tipig）均为人民币计价，符合预期。

---

## 开放分享：每个账号都可以分享（2026-10-25）

- 原实现沿用 §3.1 旧文案：签协议 → 实名 → 绑定支付宝，三项齐全才 active，未开通时整个分享者控制台被重定向回开通页——等于不是「每个人都能分享」。
- 现改为开放分享：**签完分享者协议即 active，可直接挂单**；实名与支付宝收款账户降级为「提现前」步骤，不影响挂单。
  - `nextSellerStatus` 只要求 agreement_version；`RequireActiveSeller` 仍是挂单/提现的写入门槛。
  - 提现新增收款账户校验（`ErrPayoutAccountMissing`）：未绑定收款账户申请提现返回 400「payout account is not bound」，前端映射为「提现前需要先绑定支付宝收款账户」并禁用按钮、给出绑定入口。
  - 开通页重构：未开通只显示协议一步；开通后显示「已开通，可以挂单了」+ 去挂单入口，下方是标「提现前」的可选实名/收款卡片。
  - 文案更新：lead、步骤标题/提示、状态 Badge（未开通/已开通）、错误提示；步骤条对 2/3 步标注「提现前」。
- 测试：Go 开通流程断言改为签协议即 active，新增 `TestPayoutRequiresBoundAccount`（未绑账户 400 → 绑定后进入余额校验 409）；marketplace 全量测试绿，前端 96 测试绿。
- 线上实测（普通新账号 10015，已清理）：GET /me exists=false → 只提交协议 → status=active、progress 显示实名/收款未完成 → 挂单列表 200 → 提现 400 payout account is not bound。

---

## 官方渠道白名单（OFFICIAL-CHANNELS.md，2026-10-25 完成）

### 后端
- `vendors.go` 扩展为渠道定义：`{id, display_name, tier, protocols{openai,anthropic}, models, passthrough_body, deny_paths, deny_key_prefixes, allow_host_pattern, adaptor, strip_v1_prefix}`。内置 6 家官方渠道：DeepSeek、火山方舟、阿里云百炼、Kimi（moonshot）、智谱 BigModel、MiniMax；tipig 只能经 `MP_EXTRA_VENDORS` 注入（tier=third_party），不进生产白名单。
- 出网白名单由渠道协议 URL 生成；百炼业务空间域名用锚定正则 `^[a-z0-9-]+\.cn-beijing\.maas\.aliyuncs\.com$`（`SetEgressPatterns`，非法正则直接启动失败）。
- 拒收规则：百炼 `sk-sp-` / `sk-ws-` 前缀在挂单校验阶段拒绝；火山 `/api/coding`、`/api/plan`、智谱 `/api/coding/paas/v4`、百炼 `/apps/coding` 为 deny path；内置协议 URL 在启动/挂单时校验不得命中 deny path。
- 新增 `mp_channel_model_map`（vendor + 平台模型 ID → 上游模型名，后台可编辑）：种子含 deepseek/火山/百炼的 DeepSeek 模型映射与 Kimi/智谱/MiniMax 自映射；`MP_EXTRA_VENDORS` 渠道自动补身份映射。挂单只接受「有映射 + 有官方价目」的组合。
- 云平台托管同款模型按原厂官方价计价：启动时为火山/百炼复制 DeepSeek 价目（价格与 deepseek 完全一致），广场不会出现第二个基准价。
- 验真探针用渠道的 OpenAI base，并按映射用上游模型名做最小调用；返回模型按映射反解回平台模型 ID。
- 转发：挂单渠道按 `adaptor` 选择 DeepSeek/OpenAI 类型，BaseURL 为渠道 OpenAI 协议地址；`model_mapping` 把平台模型改写为上游模型名。新增 OpenAI 适配器 MP-HOOK：`mp_strip_v1_prefix`（渠道 base 已含版本段时去掉买家路径的 `/v1`）、`mp_anthropic_messages_url` + `x-api-key`（Claude 格式请求原样转发到厂商原生 Messages 端点）。
- 新接口 `GET /api/marketplace/vendors`（公开，返回可上架模型 = 静态目录 ∩ 映射 ∩ 价目），管理接口 `GET/PUT/DELETE /api/marketplace/admin/channel-models`；管理价目列表支持 `vendor`/`tier` 过滤。
- 测试：市场包全量 3/3 稳定通过（含修正一个 Windows 时钟精度导致的偶发重复 key 夹具问题）；`go build ./...` 绿。

### 前端
- 挂单向导第 1 步分「官方渠道 / 第三方渠道（即将开放）」；官方渠道网格来自 `/marketplace/vendors`，模型列表随渠道变化，无可用模型时禁止下一步并提示联系运营配置。
- 模型广场列表/详情显示「官方渠道」标签；渠道名由接口元数据提供，未知回退 slug。
- 新增 `/admin/channel-models` 管理页（新增/更新/删除映射）。
- 文案全站统一为「官方渠道」；typecheck + 103 项 vitest 全绿。

### 说明 / 待配置
- Kimi、智谱、MiniMax 的模型目前没有平台价目，`/vendors` 会返回空 models，向导中不可选；补价目（管理价目表）后即可上架。
- 火山/百炼的上游模型名种子取自规范示意值（如 `deepseek-v3-2-*`、`deepseek-v3.2*`），上线前需用真实 key 探测并用管理页校正。
- 第二批量（百炼新加坡、混元、千帆）未启用。

---

## 模型池 / 渠道池（MODEL-CHANNEL-POOL，进行中）

### P1 完成（三张表 + 种子 + 迁移 + 管理 API）
- `mp_models`（6 个标准模型：deepseek-flash / deepseek-v4-pro / kimi-k2 / glm-5.3 / glm-4.6 / minimax-m2，含别名、能力、验真题）
- `mp_channels`（8 条：6 官方 + 火山/百炼 Coding Plan，含 key_pattern、quota_window、exhaustion_signature、条款字段）
- `mp_channel_models`（12 条可卖资格：上游名、价格系数、差异、verified/ratio/status）
- 迁移幂等：旧 `mp_channel_model_map` → 新表；价目按原厂去重；无映射旧挂单暂停并写 migration probe。
- 管理 API：`/admin/pool/models|channels|channel-models`（root，CRUD）。

### P2（别名/选路/响应/计费）
- `ModelPoolResolveAlias` 接入请求入口：glm5.3 / GLM-5.3 / zhipu/glm-5.3 → glm-5.3。
- 选路只接受 verified+active 的渠道模型与 active 渠道；套餐暂停与「只用官方渠道」开关接入（mp_key_prefs.official_only）。
- 挂单创建按渠道模型校验（有映射+原厂价目+key_pattern），channel.ModelMapping 写上游名。
- 计费：Quote 支持 OriginVendor + CoefficientMilli（官方价 × 系数 × 倍率，单次取整），PreFreezeQuote 自动解析。
- 响应改写器：市场请求的 JSON/SSE 中 `"model"` 从上游名改回标准名（只替换本次登记的上游名，换家时扩充集合）。
- 运行时渠道来源切到 DB 注册表（30s TTL，随 admin 修改生效）；`MP_EXTRA_VENDORS` 仅 `MP_ENV=test` 生效。
- 测试：别名、系数减半、未认证不可选、only-official 过滤、响应改写（JSON/SSE/换家）。

### P3 完成（探针 + 指纹抽检）
- `marketplace/fingerprint.go`：同一 verify prompt 分别打到官方渠道（平台 key，`MP_PLATFORM_KEYS`）与候选渠道，比较 `usage.prompt_tokens`，|ratio-1| ≤ 2% 视为一致；平台 key 缺失时跳过指纹、只做可用性探针。
- 巡检（每分钟）把 `fingerprint_ok` / `fingerprint_ratio` 写进 probe detail；卖家挂单探针同样回传 `verified_models`（渠道模型认证交集）。
- 连续 2 次指纹不符 → 暂停挂单（`AutoPauseListing`）；连续 3 次 → 该渠道模型资格置 `banned` 并暂停相关挂单。促销/套餐 key 即使通话成功也会因 tokenizer 差异被抓出。

### P4 完成（key 规则 + 套餐额度 + 只用官方渠道）
- 挂单只接受 `key_pattern` 命中的 key（如官方 `sk-...`），套餐 key 前缀/路径在存储前拒绝；错误信息只给规则名，不回显 key。
- 上报错误命中渠道 `exhaustion_signature` → 该渠道全部 live/canary 挂单暂停到下一个 `quota_window` 边界，记录 `pause_reason=plan_quota_exhausted` 与 `previous_status`；窗口到期由每分钟任务自动恢复，不计故障、不扣信用分。
- 买家 Key 支持 `official_only`：选路只保留 `kind=official` 的渠道模型；控制台 Key 详情可切换，API 校验并持久化。

### P5 完成（前端 + 文档）
- 广场按标准模型一模型一行：渠道数、最低价、官方价标签；详情页列出各渠道（kind/verified/系数/差异/挂单数/倍率区间）与挂单表。
- 挂单向导按渠道类型分组：官方渠道可多选（仅 `verified_models`）、套餐渠道标注额度窗口、第三方渠道禁用；渠道切换时模型集合与校验同步。
- 管理后台新增「模型池」：模型 / 渠道 / 可卖资格三个标签页的增删改查；旧 `/admin/channel-models` 跳转到新页面。
- `/docs` 快速开始自动列出当前可售模型并链接到详情页；接口描述由模型池数据驱动，不再硬编码渠道清单。
- `bun run typecheck` + 20 个测试文件 112 项 vitest 全绿。

### 部署与线上验收（2026-10-25 17:56）
- `site/deploy.sh` 重新构建并部署；重启后 `index.html` 与本地 sha256 一致（`8b0f2fb…`）。
- `/api/marketplace/vendors`：8 条渠道（6 official + 2 plan），plan 渠道 models 为空（未认证）。
- `/api/marketplace/square/models`：2 个有官方价模型（`listing_count=0`，`buyer_price_source=official`）；`deepseek-flash` 详情列出 3 个官方渠道（verified、系数 1000）。
- `/admin/pool`：6 模型 / 8 渠道 / 14 资格；`/models` 列表、`/models/deepseek-flash` 详情、`/seller/listings/new` 渠道分组均正常渲染。
- 全量后端测试在服务器 Linux 环境通过；本机 Windows 偶发 mock 端口/进程资源问题已确认为环境差异。

### 仍待 P6
- 需要平台测试 key（`MP_PLATFORM_KEYS` JSON）与卖家 key，实测六家官方渠道 + 火山/百炼 Coding Plan 入池：真实渠道探针、模型指纹、卖家挂单、官方+套餐渠道 E2E。
- Kimi / 智谱 / MiniMax 尚无官方价目，广场暂不展示；补价目后自动进入。
- 火山/百炼上游模型名为种子示意值，上线前需用真实 key 校正。

## 模型目录导入（2026-10-25）

按「最近常见模型 + 官网价格」把模型池从 6 个扩到 20 个标准模型，并为其中 17 个补齐原厂价目
（`kimi-k2`、`glm-4.6`、`minimax-m2` 暂未找到当前官网价，继续隐藏，等运营补价后自动上架）。
重启时 `SeedModelChannelPool` / `SeedPrices` 幂等补行，已有数据库无需手工操作。

| 标准模型 | 原厂 | 上下文 | 输入 | 输出 | 缓存读 | 备注 |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| deepseek-flash | DeepSeek | 131,072 | 1 / 2（峰） | 4 / 8（峰） | 0.02 / 0.04（峰） | 原有，峰谷两档 |
| deepseek-v4-pro | DeepSeek | 131,072 | 4.5 / 9（峰） | 13.5 / 27（峰） | 0.15 / 0.30（峰） | 原有，峰谷两档 |
| kimi-k3.1-pro | 月之暗面 | 1,048,576 | 13 | 68 | 1.3 | 缓存写 13 |
| kimi-k3 | 月之暗面 | 1,048,576 | 20 | 100 | 2 | 缓存写 20 |
| kimi-k2.7-code | 月之暗面 | 262,144 | 6.5 | 27 | 1.3 | 缓存写按输入 |
| kimi-k2.6 | 月之暗面 | 262,144 | 6.5 | 27 | 1.1 | 缓存写按输入 |
| glm-5.3 | 智谱 | 200,000 | 8 | 28 | 2 | 缓存存储免费（写入 0） |
| glm-5.3-flash | 智谱 | 200,000 | 0.8 | 2.8 | 0.23 | 限时五折按原价录入 |
| glm-5.3-flashx | 智谱 | 200,000 | 2 | 7 | 0.57 | 缓存写入 0 |
| glm-5.2 | 智谱 | 200,000 | 8 | 28 | 2 | 缓存写入 0 |
| glm-4.7-flash | 智谱 | 200,000 | 0 | 0 | 0 | 免费模型 |
| minimax-m3 | MiniMax | 512,000 | 2.1 | 8.4 | 0.42 | 官网 >512k 档为 2 倍，池内只售基础档 |
| minimax-m3.1-flash | MiniMax | 512,000 | 1 | 4 | 0.1 | — |
| qwen3.8-max | 阿里云百炼 | 1,000,000 | 12 | 36 | 12 | 官网未公布缓存折扣，按输入价 |
| qwen3.7-plus | 阿里云百炼 | 256,000 | 2 | 8 | 2 | 官网 >256k 档 6 / 24，池内只售基础档 |
| qwen3.8-flash | 阿里云百炼 | 1,000,000 | 0.8 | 2.7 | 0.8 | — |
| doubao-seed-evolving | 火山方舟 | 256,000 | 6 | 30 | 1.2 | 缓存存储按小时，写入 0 |

- 单位：元 / 百万 tokens；写入 `mp_price_books` 时 × 1e6 转为微元。非 DeepSeek 渠道均为
  `TierFlat`，广场按同一价格展示。
- 每个新模型都在原厂渠道生成一条 `verified=true`、系数 1000 的可卖资格，上游名 = 标准名。
- 取价来源：Kimi / 智谱 / MiniMax / 阿里云百炼 / 火山方舟官方定价页，抓取日期 2026-10-25；
  源 URL 记在 `marketplace/seed.go` 的注释里。
- 未收录的模型：`kimi-k2`、`glm-4.6`、`minimax-m2`（无当前官网价）；`kimi-k2.7-code-highspeed`
  等高速版可后续在管理后台补行。

## 验收整改（2026-10-25，P0/P1/P2）

### P0 已修
- **托管渠道结算**：结算改为 `pricingContext()` 解析原厂与系数，按原厂价目 × 渠道系数 × 倍率计价；预冻结/实扣使用同一公式与同一时刻。`QuoteResult.CoefficientMilli` 补齐。
- **非流式响应改写**：包装 writer 时删除 `Content-Length`，改写后由 Go 重新计算；同时支持上游模型名的日期/版本后缀（`-0813`、`-20251025` 等）。
- **指纹误封**：只有官方与候选都成功取得 `prompt_tokens` 才计数（`fingerprint_checked`）；欠费/429/网络错误/未比较不计入。容差改为相对 5% 或绝对差 ≤3 token，官方 prompt <20 token 视为不可判定。连续 2 次暂停该挂单，连续 3 次仍只暂停该挂单并写运维告警，**不再自动封禁渠道×模型**，封禁必须人工确认。
- **套餐额度粒度**：运行时只暂停触发耗尽的那一个挂单，不再停整个渠道；仅当上游状态码为 429/402/403 且命中 `exhaustion_signature` 才认定。
- **种子数据**：火山/百炼托管的 DeepSeek 映射不再冒充 `deepseek-v4-pro`，一律 `verified=false`；启动时会把「verified 但没有 verified_at」的非原厂行降级，真实人工验真（有 verified_at）不受影响。

### P1 已修
- 非透传渠道的 `/v1/messages` 不再把 OpenAI 格式发到厂商 Anthropic 端点；原生 Messages URL 仅在 `pass_through_body_enabled` 时启用。
- 透传仅在「所选模型上游名 = 标准名」时开启；有改名时走映射转换路径。管理后台新建渠道默认不再勾选透传。
- 管理后台修改/删除渠道映射后，已有挂单的上游 channel `model_mapping` 立即重写。
- 换 key 重新校验渠道 `key_pattern` 与 deny 前缀，套餐 key 不能换进官方渠道。
- 被平台暂停的挂单（migration/巡检/指纹/额度）卖家不能自行恢复；卖家只能恢复自己手动暂停的挂单。
- 选路价格上限按 `倍率 × 系数` 的实际有效倍率比较；系数上限收紧为 1000，渠道不允许高于官方价。
- `quota_window` 支持 `mode=rolling`（滚动窗口）和 `mode=weekly`（指定星期几 + 时分），旧零点对齐周期保持兼容。
- 挂单验证会逐个检查所选模型是否在 key 的 `/models` 列表中，并用上游名做缓存探测；平台持有原厂 key 时同步做指纹抽检，指纹不符只回 draft，不封禁。
- 管理员录入的渠道 URL 必须是 https（仅回环地址允许 http），禁止 userinfo；保存后立即刷新出网白名单。
- 迁移只暂停 live/canary 挂单并写 `pause_reason=migration`；draft/verifying 不会被变成卖家可恢复的 paused。

### P2 已修
- 官方侧指纹 token 计数按（原厂、上游模型、prompt）缓存 30 分钟，候选侧每次实调；并发安全、上限 512 条、失败不缓存。
- 别名解析改为先走 `LOWER(name)` 查询，再对 aliases 做 LIKE 缩小范围；只解析 active 模型。找不到时给出最近模型名建议。
- `OFFICIAL-CHANNELS.md` §2 已改为「套餐 key 只能进 plan 渠道」的新口径。

### 验收
- `go build ./...`、`go vet ./...` 通过。
- `go test ./marketplace/ -count=1` 通过；Linux 服务器全量 `go build ./... && go vet ./... && go test ./... -count=1` 全部通过。本机 Windows 仅 Telegram OAuth / HTTP2 / NTLM 等既有环境用例失败。
- 前端 `bun run typecheck`、`vitest 112/112` 通过。

## Key 创建改版（2026-10-25）

### 产品
- 新建 Key 从四步向导改成**一屏弹窗**：名称可选（不填由后端分配 `Key N`）、四个用途预设（均衡 / 最省钱 / 最稳定 / 仅指定模型）、默认折叠的「自定义设置」。
- 预设只是前端初始值：均衡 = 全部模型 + balanced；最省钱 = 全部模型 + cheapest；最稳定 = 全部模型 + stable + 只用官方渠道；仅指定模型 = selected + 模型勾选。
- 任意字段或花费上限被改动后，预设显示为「自定义」。
- 「仅指定模型」直接展开模型列表，可勾选并给单模型设价格上限。
- 自定义设置里包含：从已有 Key 复制设置、模型范围、路由策略、Key 价格上限、单模型上限、花费上限、RPM、优选/拉黑/只用某一家、允许回落、只用官方渠道。
- 创建成功后弹窗内直接显示完整 Key 并提供复制，可「完成」或「查看详情」；完整 Key 只显示一次。
- 详情页复用同一套 `KeyRoutingFields`，并新增模型范围展示：全部模型时隐藏逐个添加区，改为说明。

### 后端
- `mp_key_prefs` 新增 `model_scope`（all / selected，默认 selected 兼容旧 Key）；`GET/PUT /key/pref` 已透出并支持修改。
- 新增 `POST /api/marketplace/key`：一次事务写 token + KeyPref + KeyModel，任一模型或偏好不合法则整体回滚；返回 id、name、完整 key、model_scope、models。
- `model_scope=all` 的 Key 不再要求 `mp_key_models` 行，按 Key 级价格上限选路；`selected` 仍要求模型行。
- 名称留空时分配第一个未使用的 `Key N`；仍受用户 Key 数量上限约束。
- 创建接口返回完整 Key 仅此一次，日志与其它接口不输出 Key。

### 验收
- `go build ./...` / `go vet ./...` / Linux 全量 `go test ./...` 通过。
- `bun run typecheck` 通过；vitest 112/112 通过（schema 用例已补 `model_scope`）。
- 已部署；`POST /api/marketplace/key` 未登录返回 401，广场接口正常。

## Key 弹窗 v2 与厂家选模型（2026-10-25）

- 新建 Key 弹窗去掉用途预设，改为：名称（可选）→ 模型范围分段按钮（全部模型默认 / 指定模型）→ 花费上限（可选）→ 折叠的「自定义设置」（路由策略、价格上限、RPM、分享者偏好、只用官方渠道、允许回落、从已有 Key 复制）→ 创建。
- 新增 `features/market/ModelPicker.tsx`，创建弹窗与 Key 详情页共用：
  - 数据来自 `squareModelsQuery()` 与 `/marketplace/vendors`，按 `origin_vendor` 分组，不再把渠道名当厂家。
  - 厂家 Pill（全部 + 各原厂，可横向滚动）+ 模型名/别名搜索。
  - 组头 sticky，组复选框支持全选/取消/半选；行内复选框、标准名、当前档输入价、单模型价格上限（勾选后可编辑）。
  - 底部显示已选数量、清空、全选筛选结果；区分「广场暂无可售模型」和「没有匹配」两种空态。
- Key 详情页模型区复用 ModelPicker，顶部切换模型范围；保存时按差异执行 add/remove（价格上限变化走先删后加），模型范围通过 `PUT /key/pref` 保存。
- 广场列表/详情接口新增 `aliases` 字段，供 ModelPicker 按别名搜索。
- 品牌统一为 LLM Jar：`copy.brand.name`、页脚、标题、meta 全部更新；LogoMark/Logo 支持 mono 变体，登录页深色区使用白色 mono 标识；全站已无「词元市场」。
- 测试：新增 `ModelPicker.test.tsx`（厂家筛选、别名搜索、组全选/半选、上限启用、空态）与 `CreateKeyDialog.test.tsx`（默认 all 提交、指定模型空选报错、复制已有 Key 回填）；typecheck 通过，vitest 118/118 通过，Linux 全量 Go 测试通过，已部署。

## 账号、充值、Key 限制（2026-10-25）

### 忘记密码与修改密码
- 登录页密码框右侧新增「忘记密码？」入口。
- `/forgot-password`：输入注册邮箱 + Turnstile，调用 `GET /api/reset_password`；无论邮箱是否存在都提示已发送，避免账号枚举。
- `/user/reset`：邮件链接落点（与后端 `ServerAddress/user/reset?email=&token=` 对齐）。用户点击确认后调用 `POST /api/user/reset`，后端生成随机新密码并一次性返回；页面展示并支持复制，提示旧密码立即失效。
- `/console/account`：新增账号页。展示用户名/显示名称；修改密码分两步：先 `GET /api/verify/methods?scope=account.password.change` 确认密码验证可用，再用 `POST /api/verify` 换取一次性 proof，最后带 `X-Security-Proof` 调用 `PUT /api/user/self`（同时带 `original_password`）。成功后应用后端轮换的 access token，其它设备会话失效。
- 没有密码的第三方登录账号显示「未设置密码」说明，不展示修改表单；有 2FA/Passkey 但策略不接受密码验证时显示后端不可用原因。

### 支付宝充值
- 钱包页充值卡片改为真实支付：读取 `GET /api/user/topup/info`，按 `pay_methods` 展示支付方式（默认选中 alipay），金额支持 `amount_options` 快捷金额与自定义整数金额，校验全局/单渠道最低金额并显示折扣。
- `POST /api/user/pay` 返回 epay 网关地址与隐藏字段，前端用整页 POST 表单跳转；支付完成回到 `/usage-logs`，站点将其重定向到钱包页。
- 未配置在线支付时展示「暂未开放」，若配置了 `topup_link` 则给出跳转入口；兑换码流程不变。

### Key 过期时间与 IP 白名单
- `POST /api/marketplace/key` 新增 `expired_time`（Unix 秒，-1/0 = 永久）与 `allow_ips`（每行一个 IP/CIDR）；创建时校验并写入上游 token。
- 新增 `PUT /api/marketplace/key/token`，只更新这两个 token 字段；继承过期时间的 Key 在延长后自动恢复启用，手动停用的 Key 不会被误启用。
- 新建 Key 弹窗的「自定义设置」新增过期时间（datetime-local + 永不过期）与 IP 白名单（多行，支持 CIDR）；Key 详情页新增「有效期与访问限制」卡片可随时修改。
- 后端拒绝过去时间与非法 IP/CIDR，失败创建不落任何 token。

### 验收
- `go build ./...`、`go vet ./...` 通过；`go test ./marketplace/` 通过（新增过期时间/IP 白名单与更新接口用例）。
- 前端 `npm run build`、`tsc --noEmit` 通过；vitest 119/119 通过。

## 模型广场厂家选模型与加入 Key（2026-10-25）

### 厂家与筛选
- `/marketplace/vendors` 新增 `logo_url`（静态 allow-list：deepseek / zhipu / qwen / kimi / minimax / doubao；Coding Plan 渠道复用父品牌），未知厂家返回空，前端用厂家名首字圆形占位。
- 六个 SVG 放在 `site/public/vendors/`，随 Vite 构建进入 `site/dist/vendors` 并被 Go embed；DeepSeek/Qwen/Kimi/MiniMax 使用 Simple Icons 路径，智谱/豆包使用项目内简化品牌标记。
- 新增共用 `features/market/VendorPills.tsx`：logo、短品牌名（DeepSeek/智谱/千问/Kimi/MiniMax/豆包）、`N 个模型`；广场和 ModelPicker 共用，ModelPicker 不再自己维护厂家 Pill。
- 广场筛选统一走 `filterPickerModels`：只匹配标准名和 alias，不匹配厂家名或渠道名。
- `/models` 增加 URL 状态：`?vendor=deepseek&q=v3`；厂家切换保留历史，搜索输入用 replace，前后退和分享都能恢复。
- 选中厂家后出现模型名横向标签行：标准名 + 该模型所有档位中的最低输入价，点击进入详情。
- 详情页渠道表的「官方 / 套餐 / 第三方」保持只读说明，不参与广场筛选；文案明确提示到 Key 详情开启「只用官方渠道」。

### 加入 Key
- 广场表格新增行尾「加入 Key」，打开锚定小菜单：
  - 可勾选多把 Key 一次加入；已包含模型的 Key、全部模型 Key、停用 Key、未配置偏好 Key 分别显示状态且不可重复添加。
  - 全部模型 Key 不会自动改成指定模型；只做加入，不移除。
  - 模型无 live/canary 挂单时禁用已有 Key 的加入，但仍可新建 Key。
  - 未登录点击会先 `ensureAuth()`，失败跳 `/login?redirect=当前路径+查询`。
- 新增 `features/market/AddToKeyMenu.tsx`，详情页原来的单选「加到我的 Key」也换成同一多选菜单。
- 每次加入调用现有 `POST /marketplace/key/models`；多 Key 用 allSettled 汇总，`model_already_added` 视为成功，最终 toast 汇总成功/失败数量。
- `CreateKeyDialog` 新增 `initialModel`：从广场新建 Key 时自动切到「仅指定模型」并勾选当前模型，仍保留一次性完整 Key 的复制界面。

### 测试与验收
- Go：扩展 `/marketplace/vendors` 用例，覆盖六家 logo_url、Coding Plan 父品牌映射和未知厂家空值。
- 前端新增/更新：厂家 Pill 筛选与 URL 同步、URL 恢复、别名搜索不匹配渠道名、模型标签价格与详情链接、行尾加入 Key；ModelPicker logo/数量/首字占位；AddToKeyMenu 未登录跳转、多 Key 加入、新建 Key 预选模型；CreateKeyDialog initialModel。
- Playwright：390px 下厂家行/模型名行 `overflow-x: auto` 且文档不横向溢出；未登录加入 Key 跳登录并保留 redirect。

## Key 引导与官方渠道可用性（2026-10-25）

### 创建成功引导
- 创建成功页展示完整 Key 与 `base_url`（当前站点 origin + `/v1`），并为 Claude Code、Cursor、Cherry Studio 各生成一段可一键复制的配置；指定模型范围时使用第一个已选模型，全部模型范围使用 `<模型名>` 占位。
- 创建成功时查询钱包余额；余额为 0 时立即显示「先充值才能调用」并提供前往 `/console/wallet` 的链接。查询只在创建成功后启用。
- 新增 `GET /api/user/ip`（登录态），返回按可信代理配置解析的 `c.ClientIP()`；新建 Key 与 Key 详情的 IP 白名单输入框上方显示「你当前的 IP」并提供插入/复制，接口失败时静默隐藏。

### Key 过期体验
- Key 列表在到期前 3 天内显示一条提醒（只针对最近到期的一把 Key），包含 Key 名称、到期时间与详情链接。
- 判断过期同时看 `status === 3` 与 `expired_time > 0 && expired_time <= now`；过期的行降低不透明度并显示「延长」按钮。
- 「延长」弹窗接受新的 datetime-local 或「永不过期」，复用 `PUT /api/marketplace/key/token`，成功后失效 `['console','keys']` 与 `['console','key',id]`。

### 广场价格与官方可用性
- 广场列表条目新增 `multiplier_min_milli`（无挂单时为 0，沿用现有摊平聚合，无 N+1）与 `official_available`（该模型是否存在 live/canary 挂单，且渠道为 official-kind、具备 verified+active 资格）。
- ModelPicker 在价格上限旁显示「当前最低 0.xx」；输入上限低于最低倍率时提示没有可用供给。
- Key 详情在开启「只用官方渠道」时，按当前模型范围（指定模型或全部模型）列出 `official_available=false` 的模型，标题「当前设置下调不了的模型」，并链接到模型广场。
- 官方渠道无供给时，relay 的 503 错误文案补充「official-only routing is enabled」，状态码不变。

### 测试与验收
- Go：广场列表字段（最低倍率、官方可用性、未验真/套餐渠道不计入）、`GET /api/user/ip` 可信代理解析、official-only 下两种「no supplier」文案。
- 前端：创建成功配置片段与零余额提示、ModelPicker 最低价提示、Key 列表到期提醒与延长、Key 详情官方渠道不可调用模型提示、当前 IP 插入。

## 分享者端体验补齐（2026-10-25）

### 异步验真进度
- 后台验真仍由创建/换 Key 后的 goroutine 执行，`sellerVerifyTimeout` 固定为 60s；卖家列表与详情在任意挂单为 `verifying` 时以 3s 间隔自动刷新，全部离开该状态后自动停止。重新检测成功后先把本地缓存行标记为 `verifying`，避免请求先到、后台状态尚未翻转时停止轮询。
- 列表与详情读取最近一次 `type=verify` 探针的 `detail.model_results`，逐个模型展示 `ok / upstream_model / latency_ms / fail_reason`；失败的模型单独列出，英文失败原因（401、余额、超时、网络、模型不在 /models 等）统一翻译为中文，并补充整体失败原因。
- 验真收窄可调用模型后写回 `mp_listings.models` 时保持 JSON 数组（`Listing.BeforeUpdate` 兜底裸列更新，避免出现裸值或「row value misused」）。
- 待验真（draft，含验真失败回退）的挂单提供「重新检测」，调用 `POST /seller/listings/:id/verify`；`verifying` 期间按钮禁用。

### 重复 Key 区分
- 指纹重复时后端返回稳定 code：自己的挂单返回 `DUPLICATE_KEY_OWN` + `listing_id`（前端给出「你自己已上架过」并链接到 `/seller/listings/$id`）；其他账号的挂单只返回 `DUPLICATE_KEY_OTHER`，响应不含对方 seller/listing 标识，同时通过 `common.SysError` 写运营告警。
- 换 Key 路径复用同一套判定与文案。

### 套餐渠道
- `CreateListingInput` / `POST /seller/listings` 新增 `plan_terms_accepted`；`kind=plan` 且未显式传 true 时库层返回 `ErrPlanTermsRequired`、接口返回 `PLAN_TERMS_REQUIRED`。
- 向导在选中套餐渠道时展示 5 小时 / 每周窗口说明（来自 `/marketplace/vendors` 的 `quota_window` 与 `terms_url` / `reset_timezone`），并要求勾选「我确认该套餐条款允许共享」后才能提交。
- 因额度耗尽被平台暂停的挂单，列表与详情展示翻译后的暂停原因与 `resume_at` 预计恢复时间；平台暂停不允许卖家手动恢复。
- 模型缺失说明：向导把该渠道已认证但 Key 的 `/models` 未暴露的模型列为不可勾选，并逐名提示「不在这个 Key 的 /models 列表中」。

### 收益预估与提现门槛
- `/marketplace/status` 暴露后端唯一费率常量 `platform_fee_percent`（当前 7%），向导按该值实时展示买家价（官方价 × 渠道系数 × 倍率，系数来自 probe 接口）、平台费与到手价（BigInt 整数微元，避免浮点误差）；市场最低倍率来自广场详情 `multiplier_min_milli`。
- 分享者概览新增提现条件进度卡：实名认证、绑定支付宝、可提现满 ¥50、T+7 解冻。实名/支付宝/余额使用 `/me` 与 `/seller/stats` 的真实状态，T+7 在仍有待结算金额时按静态要求展示，不虚构逐笔解冻日期。

### 测试
- 前端：`src/features/seller/{errors,api,wizard,preview,payout-gate}.test.ts` 与 `src/app/routes/seller/listings/new.test.tsx`，覆盖重复 Key 两种 code、套餐勾选门槛、收益/最低倍率、提现进度与 3s 轮询开关。
- Go：`marketplace/api_seller_duplicate_test.go` 覆盖 `DUPLICATE_KEY_OWN/OTHER`（外部账号不泄露 id）、`PLAN_TERMS_REQUIRED`、重新检测状态机，以及列表/详情返回 `verification.model_results`、`pause_reason`、`resume_at` 和套餐窗口字段。

## 充值安全、验真与重复 Key（2026-10-25）

### 充值金额与订单
- 启动时 `enforceMarketplaceBillingInvariants` 强制 `operation_setting.Price = 1` 并写回 `Price` 选项；原值不是 1 时用 `SysError` 打告警。新用户/邀请人/被邀请人赠送额度同样强制为 0，避免赠送余额经自买自卖变成可提现收入。
- 钱包充值改为先调用 `POST /api/user/amount` 取后端报价，页面显示「实付 ¥X」；支付按钮在报价返回前不可用，前端不再自行乘费率。
- `POST /api/user/pay` 响应新增 `trade_no`，支付前写入 `sessionStorage`。epay 回跳地址改为 `/console/wallet?topup=returning`；回跳后以 3 秒间隔轮询 `GET /api/user/topup/self` 30 秒：成功提示已到账并刷新余额，超时提示「已付款但尚未到账」并给出订单号。
- 钱包新增「充值记录」表：订单号、到账金额、实付金额、状态（待支付 / 已到账 / 已关闭）与下单时间，供客服对账。

### 计费与选路
- `SelectInput` 新增 `BuyerID`；`SelectWithProbe` 跳过 `seller_id == buyer_id` 的挂单，relay 首次选路和兜底换家都传入买家用户 id，防止用自己的赠送/兑换余额买自己的挂单。
- 官方渠道 503 文案在 Key 开启「只用官方渠道」时明确说明是该开关导致没有可用渠道。

### 卖家验真
- `VerifyListing` 对每个已选模型各发一次真实的 `max_tokens=1` 请求；`/models` 里有但实际调用失败的模型单独记录在 `model_results`，只把通过的模型写回挂单并允许上架；全部失败则回退 draft。
- `keyExposesModel` 只接受精确名称或 6/8 位纯数字日期后缀（如 `-20251001`），`glm-5` 不再匹配 `glm-5.3`、`-flash`、`@beta`、`_latest`。
- 验真探针详情新增 `model_results` 与 `listed_models`；卖家列表/详情展示逐模型结果和中文失败原因，并支持「重新检测」。

### 其他
- `/api/status` 新增 `smtp_configured`，登录页仅在该值为真时显示「忘记密码？」。
- 说明：当前未实现「充值/赠送余额拆分」，而是把三类可配置赠送额度强制为 0 并排除自买自卖；管理员发放的兑换码仍是显式运营行为。

## 站点结构整理：一个控制台与术语统一（2026-10-26）

### 统一控制台
- 删除买家/分享者身份切换和三套侧边栏，改为 `/console` 下一条分组侧边栏：使用（概览/API Key/使用记录）、资金（钱包）、分享（我的渠道/收益与提现，开通后显示）、账号、运营（定价/模型/审核/工具，管理员显示）。
- 顶部导航改为「模型广场 · 文档 · 分享额度」；新增公开 `/share` 分享额度介绍页。未开通分享的用户在概览看到「把闲置额度分享出来」卡片，开通引导不再常驻侧边栏。
- 卖家页面迁入 `/console/listings`、`/console/earnings`、`/console/onboarding`；旧 `/seller/*` 全部跳转。运营页面迁入 `/console/ops/pricing|models|review|tools`；旧 `/admin/*` 全部跳转；`/ops-legacy` 是后端鉴权的旧后台入口，保持原样，新运营功能在 `/console/ops`。
- 390px 侧边栏改为 Radix Dialog 抽屉；桌面保持 220px sticky 分组侧栏。

### 术语与文案
- 买家/分享者文案禁用「挂单、系数、微元、倍率区间、聚合数据、不编造、尚未接入、接口错误、格式转换、运营工具」；运营后台保留内部术语。
- 新增 `copy-terms.test.ts` 遍历买家/分享者文案，禁用词命中为 0；`console-nav.test.ts` 覆盖买家/分享者/管理员三种侧边栏分组。
- 模型广场买家侧去掉「最低倍率」列；首页统计改为「最高可省」百分比；Key 创建的价格上限提示改为「当前最低 ¥x / 百万」。

### 模型详情
- 自检修复：模型详情峰谷标签只显示一次；运营价目输入支持小数（`inputMode=decimal`）；移动端控制台顶栏显示当前页面名；侧边栏「账号设置」不再与分组名重复；Key 路由设置里不再出现「倍率」字样。
- 运营价目/按次价表单改为按「元」输入，提交时转成后端整数微元；表格仍统一走 `formatYuan` 展示。
- 首屏只保留一个主价格（当前 ¥x / 百万 tokens），下方「官方 ¥y，省 z%」；峰谷标签保留。
- 官方价目表收进「价格明细」折叠区；渠道信息收进「渠道信息」折叠区，买家侧不再显示系数和倍率区间。

### 钱包与记录
- 新增 `GET /api/marketplace/wallet/transactions`，把 mp_ledger 消费/退款与 top_ups 充值按时间合并分页；钱包页改为「全部 / 消费 / 充值」三 tab 的统一流水。
- `/console/records` 增加「我的调用 / 我卖出的」tabs，分享记录从卖家后台移入使用记录；`/console/earnings` 只保留收益概览与提现记录。
- 运营模型池的「渠道模型映射」tab 支持 `?tab=channel-models` 深链，不再是孤儿页。

### 验收
- 前端 `tsc --noEmit`、`vitest`（30 文件 / 164 测试）、`vite build` 通过；Go `build`/`vet` 通过，marketplace 全量测试除一次本地 mock 网络抖动外通过，重跑失败用例通过。
- 旧路径跳转已用生产构建预览验证：`/seller` → `/share`，`/seller/records` → `/console/records?view=sales`，`/admin/prices` → `/console/ops/pricing`，`/admin/channel-models` → `/console/ops/models?tab=channel-models`；`/ops-legacy` 是旧后端入口，不在 SPA 路由内。

## 第三方渠道上架：codely / clinepass（2026-10-26）

### 注册与协议
- codely、clinepass 以 `kind=third_party` 注册进 mp_channels（slug 9/10），协议指向本站网关反代：`https://llmjar.com/gw/codely/v1`、`https://llmjar.com/gw/clinepass/v1`（nginx `/gw/*` → 127.0.0.1:8792 / 3126）。
- 协议 URL 必须带 `/v1` 段：验真按 `base+"/models"`、`base+"/chat/completions"` 拼接（与 moonshot `/v1`、ark `/api/v3` 同规则）；漏掉 `/v1` 时 codely 网关把 `/models` 转给 dsh-bridge 返回 403→502。
- 两个渠道都开 `strip_v1_prefix`：买家请求的 `/v1` 前缀被去掉，中继最终请求 `base + /chat/completions`，不会出现 `/v1/v1`。
- 出网白名单通过 `/etc/llmjar.env` 的 `MP_EGRESS_DOMAINS` 显式固定（官方域名 + llmjar.com），验真不再依赖 registry TTL 刷新。

### 挂单与验真（snake 账号下）
- listing 7（codely，倍率 0.5）：glm-5.3→codely-core、deepseek-v4.1-flash→codely-flash，逐模型验真通过 → canary。
- listing 8（clinepass，倍率 0.5）：deepseek-v4.1-flash-1017→cline-pass/deepseek-v4.1-flash-1017，验真通过 → canary。
- 挂单向导开放第三方渠道分组（前端已部署），卖家粘贴网关 Key（codely 渠道 Key / clinepass proxyKey）即可上架。

### 端到端验收
- 用 e2ebuyer（10012）的临时 Key 分别调用 glm-5.3（codely）与 deepseek-v4.1-flash-1017（clinepass）：HTTP 200，mp_requests 记录成功，账本三向一致（buyer_debit = platform_fee + seller_credit，卖家收益 7 天后可提）。
- 公开模型广场 `/api/marketplace/square/models` 已列出三个模型及其渠道数；canary 挂单按 10% 灰度放量，24h 成功率 ≥98% 后由任务自动转 live。
