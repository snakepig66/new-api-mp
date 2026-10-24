# new-api-mp 代码审查（分支 `mp`，b2da4cb4 → f486440c）

审查日期：2026-10-24 · 基线：上游 33c61a24 · 需求来源：handoff/MARKETPLACE-DEV.md、docs/marketplace/PROGRESS.md

## 结论

**现在不能上线收真钱。** 底层的金额函数写得很扎实：微元、128 位/big.Int 运算、7% 四舍五入、恒等式、`(request_id, entry_type)` 唯一索引都对。也就是说，Quote、money、ledger 单独看是对的。问题出在把它们串起来的请求链路和资金闭环上：

- 冻结和并发槽在几条提前退出的路径上会泄漏。一个余额为 0 的买家就能把全部挂单打成 503。
- 熔断半开探测在失败后不归还，挂单会永久被拉黑。
- 账本上记给卖家的钱可能根本没从买家那里收到。
- `/v1/messages` 的缓存 token 被重复扣减，买家少付钱。
- 买家与卖家共用同一个账本账户，卖家收入会被自己的消费重复扣掉。
- Key 的花费上限，以及优选、拉黑、只用某一家这几项偏好，在路由和扣费里完全没生效。
- 最新提交把第三方网关 tipig 写进了生产白名单。e2e 脚本里硬编码了管理员口令。

下面「高」级的问题要先修完，再做真实 key 的端到端测试。

## 构建与测试（本次实测）

- 环境：Go 1.27.3 linux/arm64（`go.mod` 要求 1.26.10）。构建时 `web/dist` 用一个占位 index.html。
- `go build ./...`：通过。
- `go vet ./marketplace/...`：通过。
- `go test ./marketplace/... -count=1`：209 个顶层测试全部通过（SQLite）。
- `-race`：没跑。沙箱里没有 gcc，CGO 不可用。
- 密钥扫描范围：15 个 mp 提交的全部新增行，加上当前工作树。上游历史是 blob:none 部分克隆，没有逐提交扫描。
  - 没发现真实的 `sk-` key、带密码的 DSN 或 `.env`。测试里的 `sk-api…` 都是占位符。
  - 发现一处硬编码口令，见 H9。

---

## 一、问题（按严重度排列）

### 高

**H1 冻结、并发槽、半开探测在提前退出路径上泄漏，零余额买家就能把全站打成 503**
- 位置：`marketplace/relayhook.go:335-370`（选中就占槽）；`controller/relay.go:126-134, 137-140, 163-168`；`relay/request_billing.go:79-82`。
- 问题：槽在 Distribute 阶段就占了，但只有 `SettleRelay` 或 `FailRelay` 会释放它。而 `FailRelay` 只在 `RefundFailedRequestBilling` 里调用：这个 defer 在 `PrepareRequestBilling` 成功之后才注册，并且 `apiErr==nil` 时直接 return。下面三种情况都绕过了它：
  - 请求体校验失败（400）；
  - `GenRelayInfo` 失败；
  - `PrepareBilling` 返回 402 余额不足或 400 无价目。
  
  这三种情况下都不释放槽、不写失败记录。handler panic 时，newAPIError 是 nil，**预冻结永远不退**，槽也要等 10 分钟才释放。
- 失败场景：一个余额为 0 的账号每秒发 5 个 `deepseek-flash` 请求，每个请求都先占住最便宜那家挂单的一个槽再返回 402。几秒内所有挂单的槽都被占满，10 分钟内全站返回「暂无可用供应商」，攻击成本为 0。如果 panic 发生在流式请求中途，买家的冻结金额会永久丢失。
- 修复：在 `middleware/distributor.go:147` 的 `c.Next()` 外面加一个 `defer marketplace.EndRelay(c)`（要能感知 recover）。会话没结算也没失败时，统一做这几件事：
  - `Billing.Refund`；
  - 释放槽和半开探测；
  - 写失败记录。
  
  `FailRelay` 和 `SettleRelay` 保持幂等。补测试：402、坏 JSON、panic 三种情况下，槽数和余额都要回到原值。

**H2 熔断半开探测失败后不归还，挂单被永久拉黑**
- 位置：`marketplace/selector.go:165-194`；`relayhook.go:257-262`（`finishAttempt` 只释放槽）；`record.go:208`。
- 问题：429 冷却 60 秒后进入半开状态。被选中的请求拿到探测权，但只有 429（`Record429`）或成功结算（`RecordSuccess`）才会释放它。如果探测请求以 5xx、超时、401 或 H1 里的任一提前退出路径结束，`probeInFlight` 会一直是 true，`allow` 永远返回 false。
- 失败场景：某卖家先被 429 一次，60 秒后的探测请求恰好遇到上游 502。此后直到进程重启，这条挂单都拿不到流量，卖家和后台都看不到原因。
- 修复：会话里记下 `tookProbe`；在 `finishAttempt`、`FailRelay` 和 EndRelay 里，非 429 的失败调用 `releaseProbe`（或把失败计入熔断）。

**H3 账本给卖家记的钱，可能没从买家那里收到（冻结不足 + 结算透支 + 账本与余额不在同一事务）**
- 位置：`marketplace/billing.go:138-161`；`record.go:186-203`；`relayhook.go:516-519, 573-585`。
- 问题有四处：
  1. 买家没传 `max_tokens` 时，`affordableMaxOutput` 只按余额压低冻结额，但**没把 max_tokens 写进上游请求**，上游的实际输出不受限制。
  2. 余额不够付输入时，`remaining<=0` 返回 0，按只冻结输入放行，没有返回 402。
  3. `remaining*1e6*1000` 在余额达到约 ¥9,223（9.22e9 微元）时溢出 int64，结果变成负数，被夹成 0，于是大余额买家的输出冻结额恒为 0。
  4. 结算时补扣不足会「扣到 0 为止」，只记一条日志。但 `SettleRelay` 在这之前已经按完整的 `TotalDebit` 写了 buyer_debit 和 seller_credit。
  
  另外，§4.3 第 5 步要求「在一个事务内写流水、释放冻结、写记录」，现在这三步分开执行，`PostSettlement` 出错也只记日志。
- 失败场景：
  - 买家余额 ¥0.02，发一个不带 max_tokens 的长回答请求，实际费用 ¥0.5。余额被扣到 0，但 seller_credit 按 0.5×93% 记账，T+7 后可以提现，平台白赔 ¥0.465。
  - 大余额买家并发 500 个请求，每个只冻结了输入，同样会触发透支。
- 修复：
  - 把 `maxOut` 写回出站请求（chat 的 `max_tokens` / `max_completion_tokens`，responses 的 `max_output_tokens`）。
  - `remaining<=0` 时返回 402。
  - 乘法改用 `mulDivCeil` 或 big.Int。
  - 先执行 `Billing.Settle` 拿到实际收到的金额，再在**同一个 DB 事务**里写 mp_ledger、mp_requests，并更新 users.quota。实收不足时，按实收金额记 buyer_debit，再加一条 shortfall 行，或者直接拒绝给卖家记账。

**H4 `/v1/messages` 把缓存 token 重复扣减，输入少收钱**
- 位置：`marketplace/record.go:50-60`。
- 问题：Anthropic 语义的 usage 里，`input_tokens` 本来就**不含** cache_read 和 cache_creation。上游 `service/text_quota.go:312-322` 用 `IsClaudeUsageSemantic` 区分了这一点。`extractMarketplaceUsage` 却一律执行 `PromptTokens - cacheRead - cacheWrite`，结果小于 0 时夹成 0。
- 失败场景：Claude Code 走 DeepSeek Anthropic 端点，返回 `input_tokens=3000, cache_read=60000`，计费输入变成 0。3000 个未命中缓存的输入 token 不收费。缓存命中率越高的用户，少收得越多。
- 修复：`usage.UsageSemantic=="anthropic"`，或者 `info.GetFinalRequestRelayFormat()==Claude` 时，不做扣减。补一个 messages 路径带缓存的对账测试。

**H5 买家消费和卖家收入记在同一个账本账户；冲正只写了账本，没退到真实余额**
- 位置：`marketplace/ledger.go:95-130`（buyer_debit 和 seller_credit 都记在 `user:<id>`）；`ledger.go:263-286`；`payout.go:67-81, 145-155`。
- 问题有两处：
  1. 可提现余额 = `AccountBalance("user:<id>", available)`，其中混进了这个人作为买家的 buyer_debit 和 tool_debit（负数）。但买家花的钱已经从 users.quota 里扣过一次了。
  2. `Reverse` 只追加一条 `refund(+total)` 账本行，**从来没有调用 `IncreaseUserQuota`**，买家并没有真正拿回钱。而且这条 refund 行记在 `user:<buyer>` 的 available 下，买家如果同时是卖家，它就变成了可提现现金，绕过了「充值不退」的规则。
- 失败场景：
  - 卖家 #10023 有 ¥100 可提现，又用自己的 Key 买了 ¥30，结果可提现只剩 ¥70，等于被重复扣了一次。
  - 后台对一笔 ¥40 的请求做冲正：买家余额不变，但这个账号如果开通了分享者身份，就能多提现 ¥40。
- 修复：拆成 `buyer:<id>` 和 `seller:<id>` 两类账户。提现只看 seller 账户。冲正时在同一事务里 `IncreaseUserQuota`，refund 行记到 buyer 账户（不可提现）。

**H6 买家 Key 的路由偏好全部没生效（§3.6）**
- 位置：`marketplace/selector.go:351-452`；`relayhook.go:335, 635`。
- 问题：`KeyPref` 的 Strategy、PreferSellerIDs、BlockSellerIDs、OnlySellerID、AllowFallback 只在 api_key.go 里存取。`Select` 只按倍率和延迟排序，根本不读这些偏好。
- 失败场景：买家拉黑了 #10023，请求照样转发给 #10023；设置了「只用 #10011」，也会被路由到别家。这既违反需求，也违背买家的明确指示，属于隐私和合规问题。
- 修复：`SelectInput` 增加偏好字段。过滤顺序依次是拉黑、只用某一家（不可用时按 AllowFallback 决定是报错还是回落）、优选（全部不可用时回落全量），strategy 决定打分权重。换家时同样适用。

**H7 Key 的花费上限 / 令牌额度被完全绕过**
- 位置：`marketplace/billing.go:86-190`。对照：上游 `service/billing_session.go:69-71, 222` 会同时扣减令牌额度。
- 问题：`BillingSettler` 接口约定的是「同时调整资金来源和令牌额度」，但 `BillingSession` 只动 users.quota，tokens 表的 remain_quota 和 used_quota 从不变化。§3.6 的「花费上限」在 mp 表里也没有对应字段。
- 失败场景：用户给某个 Key 设了 ¥10 上限并发给同事用，这个 Key 实际可以花光账户全部余额。Key 泄露后损失没有上限。
- 修复：冻结、结算、退款时，同步调用 `PreConsumeTokenQuota`、`DecreaseTokenQuota`、`IncreaseTokenQuota`（非 unlimited 的令牌），或者在 KeyPref 里加一个 spend_cap 并在冻结时原子检查。

**H8 tipig 网关进了生产白名单，违反「只接官方渠道、base_url 固定」，而且地址拼出来是错的**
- 位置：`marketplace/vendors.go:38-43`；`marketplace/seed.go`（提交 f486440c 新增 tipig 的 4 行价目）；`listing.go:135-139`。
- 问题：
  - 第三方或自有的 new-api 中转被当成和 DeepSeek 同级的供应商放进生产代码。出网白名单会从它自动派生 `api.tipig.cloud`，任何用户都可以挂 tipig 的 key 卖。
  - 模型名 `deepseek-v4.1-flash-1017` 和 `deepseek-v4-pro-0813` 不是官方 ID，却套用了官方价，正好踩中 §9「网关静默换模型」的坑。
  - 渠道类型固定是 DeepSeek，适配器会在 base_url 后面拼 `/v1/chat/completions`。base_url 已经以 `/v1` 结尾，于是 chat 请求发到 `…/v1/v1/chat/completions`，messages 请求发到 `…/v1/anthropic/v1/messages`，基本不可用。
- 修复：从 `vendors` 和 seed 里删掉 tipig。如果 E2E 需要，就通过只在测试构建里生效的 build tag，或者 `MP_TEST_VENDORS` 环境变量注入，并且默认关闭。
- 补充：仓库里没有发现 tipig 的 key 泄露。

**H9 公开仓库里硬编码了管理员口令**
- 位置：`docs/marketplace/e2e.sh:9`（提交 24a2062a 引入）。
- 问题：脚本用一个固定口令通过 `/api/setup` 初始化 root 账号，同一个口令也用于买家和卖家测试账号。PROGRESS 记录脚本已经在 llmjar 上 staged，而 llmjar 的 3000 端口通过 instabox 公网可访问。
- 失败场景：任何读过这个仓库的人，都能用 admin 和这个口令登录测试站后台，进而审批提现、反查买家代号、导出数据。
- 修复：立刻在 llmjar 上修改 root 口令（如果已经初始化过）。脚本改成从环境变量读口令。git 历史可以选择用 filter-repo 清理，但口令必须视为已泄露。

### 中

**M1 「首字节 20 秒」实际上是 ResponseHeaderTimeout，语义不对**
- 位置：`marketplace/relayhook.go:909-919`。
- 问题：DeepSeek 会先返回响应头，然后持续发送 keep-alive（非流式发空行，流式发 `: keep-alive`）。所以响应头很快就到了，20 秒等于没设，上游在发完头之后卡住也不会触发换家。反过来，对于那些生成完才发响应头的非流式上游，长推理请求会在 20 秒时被误杀并换家，而每一家都会为这次生成向卖家收费。
- 修复：在 body 上包一层 reader，以「第一个有效数据块」（SSE 的 data 帧或非空 JSON）为准计时，超时就 cancel context 并返回可重试的错误。

**M2 换家预算依赖可变的全局变量 `common.RetryTimes`**
- 位置：`marketplace/marketplace.go:57-59`；上游 `model/option.go:656`（`SyncOptions` 会周期性从 DB 覆盖这个值）；`controller/relay.go:180`。
- 失败场景：管理员在后台保存一次运营设置（RetryTimes=0），或者 options 表里本来就有这一行，同步之后循环只跑 1 次，市场请求再也不会换家，§3.7 整体失效，而且没有任何报错。
- 修复：在 `getChannel` 和循环条件里，市场请求使用自己的上限（2 次换家 + 1 次原地重试），不再读全局值。或者在 option 更新钩子里强制 MP 开启时取 ≥2。

**M3 SQLite 上的时间比较是字符串比较，结果随时区变化（已用代码实测）**
- 位置：`marketplace/seed.go:41-42`（+08:00 写入）；`pricebook.go:33, 52`；`selector.go:246-251`；`api_admin.go:126-134`。
- 问题：glebarez/sqlite 把 time 存成带时区偏移的文本，`effective_at <= ?` 按字典序比较。实测：生效时间按 CST 写入，查询时刻用等价的 UTC 表示，01:00 CST 这一刻查不到价目。
- 失败场景：llmjar 当前用的就是 SQLite。服务器时区是 UTC 时，`time.Now()` 也是 UTC，于是：
  - 2026-11-01 的 Search/Fetch 按次计费会推迟 8 小时生效；
  - 每日金额上限的「当天」窗口偏移 8 小时；
  - 后台用不同时区写入的价目版本，排序会出错。
- 修复：所有写入和查询的 time 统一 `.UTC()`（包括 gorm `NowFunc`），或者存 Unix 秒。补一个 TZ=UTC 的回归测试。

**M4 Search/Fetch：11-01 前出现次数会导致整单免费；上游已经能读到的次数被丢弃，§3.4 实际上永远不会计费**
- 位置：`marketplace/record.go:113-118, 150-181`；`quote.go:156-171`；`service/text_quota.go:422`；`relayhook.go:850-852`。
- 问题有两处：
  1. usage 里有 search 次数但冻结时刻没有按次价时，`Quote` 返回 ErrNoPrice，被判定为「结算失败」，**token 费也一起变成 0**。11-01 之前，以及 10-31 23:59 冻结的请求都会这样。
  2. 上游的 DeepSeek 适配器已经能读到次数（`relay/channel/deepseek/adaptor.go:64-72`：messages 路径读 `server_tool_use`，responses 路径数已完成的 `web_search_call`），汇总在 `summary.ToolSurchargeItems` 里。MP-HOOK 却把它置成 nil，自己再从 `dto.Usage.ServerToolUse` 读，而这个字段只在 OpenAI 格式的解析路径上有值。同时 `vendorToolCountsReadable` 恒为 false，所有服务端搜索工具都被剥离，§3.4 的计费功能实际上是死代码。Claude Code 和 Codex 用户会失去网页搜索能力。
- 修复：没有按次价时，工具计 0、token 照常收费，并记一条告警事件。次数改从 relayInfo 的工具计数（`deepseek_search` / `deepseek_fetch` 两个键）读取。DeepSeek 在 messages 和 responses 路径上把 readable 设为 true，只在确实读不到的路径上剥离。

**M5 同一把 key 加空格就能重复上架**
- 位置：`marketplace/keycrypto.go:89-95`；`listing.go:63, 111-115`。
- 问题：校验时用 TrimSpace 判空，但指纹和加密都用原始字符串计算。
- 失败场景：`"sk-abc"` 和 `"sk-abc "` 的指纹不同，可以挂两次。HTTP 头部值的首尾空白在上游会被忽略，两条挂单都能用。这样既能绕过「全平台只挂一次」，也能把这把 key 的并发和 RPM 上限翻倍。
- 修复：先对 key 做规范化（TrimSpace，拒绝内部空白和控制字符），再算指纹和加密。

**M6 canary 和可用率统计失真；canary 挂单无法自动暂停；恢复时不重新验真**
- 位置：`record.go:289-301`（记录只归属最后一家挂单）；`jobs.go:143-157`；`listing.go:247-254`；`selector.go:517-519`。
- 问题有四处：
  1. 一次请求先在 canary 挂单 A 上失败，换到 B 成功，记录只记在 B 名下，A 的失败从统计里消失了。
  2. 自买自卖的请求被计入成功率，违反 §3.5。卖家可以自己刷 20 次成功，把挂单推成 live。
  3. `PauseListing` 只允许 live→paused，所以 canary 状态的 key 失效或余额不足时，`AutoPauseListing` 会失败，挂单继续接收流量。
  4. 卖家 resume 时直接回到 live，被自动暂停（key 失效）的挂单不用重新验真就能恢复。
- 修复：每一跳的结果按挂单分别记录（mp_probes，或者重试链按 listing 聚合）。统计时排除 `buyer_user_id = seller_id`。canary 也允许进入 paused。自动暂停过的挂单恢复时走 verifying。

**M7 RPM 在所有候选挂单上都扣，导致误报 503**
- 位置：`marketplace/selector.go:402-407`。
- 问题：每个通过前置过滤的候选挂单都会扣一次 RPM，没被选中的那些也扣了。
- 失败场景：某模型有 5 家挂单，各自 RPM=60。全站 60 个请求就会耗尽**所有** 5 家的 RPM，即使后 4 家一单都没接。第 61 个请求直接 503，而实际还有 4 倍的余量。
- 修复：只对胜出者扣 RPM，用「先看后扣」或者给落选者退回额度。另外，每个请求都对每个候选查一次当日金额，并且全表加载挂单（连同密文一起），建议加缓存。

**M8 对外错误码没有收敛到 §3.10**
- 位置：`relayhook.go:314, 325, 341, 354`（仍然返回 500，与 PROGRESS「已改 503」的说法不一致）；`controller/relay.go:229-233`。
- 问题：换家预算用完、或上游策略判定不重试时，最后一家的上游状态码和消息原样返回给买家。
- 失败场景：卖家 key 失效时上游返回 401，买家看到的是「401 无效 Key」，会误以为是自己的 Key 有问题。上游的错误文本也可能带出卖家 key 的尾号或账户信息。
- 修复：市场请求的最终错误统一映射到 400、402、503 三种，消息用固定文案，上游原文只写进重试链和日志。

**M9 分享者开通流程不存在，提现在生产环境里永远失败**
- 位置：全仓库没有任何代码创建 `mp_sellers` 记录；`payout.go:118-123`；`api_seller.go:154-207`。
- 问题：任何登录用户都能挂 key，没有签协议、实名、绑定支付宝这些步骤。`RequestPayout` 要求 mp_sellers 里有记录，否则返回 ErrPayoutNotFound，接口显示为 400。测试是直接往表里插记录才通过的。
- 修复：补上开通 API 和状态机（协议版本 → 实名 → 收款账户加密存储）。创建挂单和申请提现都要求卖家 status=active。

**M10 流式中途断开或截断一律不收费，可以被白嫖**
- 位置：`marketplace/record.go:132-146`。
- 问题：这一条与 §10-1「默认不收费」一致，但属于必须由产品拍板的风险：买家等内容基本输出完，在 `[DONE]` 之前主动断开，就能免费拿到完整回答。上游照样按 token 向卖家收费，卖家亏本。
- 建议：至少在已经收到 finish_reason 或 usage 帧时按实际 usage 计费，只对真正的截断（没有任何 usage）免单。同时监控单个买家的断流率。

**M11 价目和日历的管理问题**
- 位置：`api_admin.go:126-134, 205-221`。
- 问题有四处：
  1. 价目的 effective_at 可以填过去的时间。回溯插入新版本后，同一条请求的预冻结和结算会落在不同版本上，违反 §3.3「同一价目版本」。
  2. tier 字段没有校验。
  3. 节假日导入后不会立即 Reload，最长要等 1 小时才生效。
  4. 没有按次价（search/fetch）的后台接口，也没有「下一年节假日没导入」的告警。春节如果漏导入，会按高峰价收 2 倍。
- 修复：effective_at 要求不早于当前时间，tier 做枚举校验，导入后立即 Reload，增加 per-call 价目接口，年初检查日历是否已导入。

**M12 入站剥离的副作用**
- 位置：`marketplace/servertools.go:26-37, 60-84`。
- 问题有三处：
  1. 用 `map[string]any` 重新序列化，会打乱字段顺序（上游刚修过 JSON Schema 属性顺序的问题，见 d0b840e7），大整数也会丢精度。
  2. 剥离后 `tool_choice` 如果还指向 web_search，上游会返回 400。
  3. 没处理 chat 的 `web_search_options`。
- 修复：用 sjson/gjson 只删数组元素；同步清理 `tool_choice`；把 `web_search_options` 纳入剥离范围。

**M13 买家 RPM 按 token 计而不是按买家计，默认不限**
- 位置：`marketplace/keyprefs.go:170-182`。
- 问题：一个买家多建几个 Key 就能绕过 RPM 限制，平台层面也没有默认值，与 §3.6「每个买家有自己的 RPM 限制」不符。
- 修复：按 user_id 计数，并设平台默认值。

### 低

- **L1** `SlotManager.Release` 释放的是最早的那个槽，不一定是当前请求的（`selector.go:67-80`）。超过 10 分钟的长流式请求的槽已经被清扫，结束时会释放掉别人的槽，导致并发超限。修复：Acquire 返回句柄，Release 时按句柄释放。
- **L2** 出网 IP 黑名单缺了这些段：0.0.0.0/8、192.0.0.0/24、192.0.2.0/24、240.0.0.0/4、255.255.255.255、64:ff9b::/96（NAT64）、2002::/16（6to4 内嵌私网）（`egress.go:95-108`）。因为 base_url 是固定的，实际风险较低，但建议补全。
- **L3** `Reverse` 更新 seller_credit 时没检查 RowsAffected（`ledger.go:242-246`）。在 SQLite 上它和 T+7 转正是可以交错执行的。
- **L4** 分享记录详情返回完整的重试链（`api_records.go:318-334`），里面有其他卖家的 listing_id 和错误事件，存在跨卖家信息泄露。
- **L5** 提现审批、冲正、代号反查只要求 AdminAuth，建议改成 Root，并禁止管理员审批自己的提现。
- **L6** `relaykit/dto/openai_response.go` 新增的 `ServerToolUse` 字段在 MP 关闭时也生效，会让 OpenAI 格式响应里多出 `server_tool_use` 字段被透传。另外 relaykit 是独立模块，以后同步上游时容易冲突。
- **L7** 槽、熔断、RPM 都是进程内状态，jobs 在每个节点上都会跑。多节点部署前必须把这些状态迁到 Redis（代码注释里已经说明）。

### MP_ENABLED=false 时对上游的影响

逐处核对了 9 个接缝文件。`BeginDistribute`、`IsRelayRequest`、`MayRetry` 等 hook 在 MP 关闭时都是空操作，路由也不会注册，行为与上游一致。唯一无条件生效的改动是 L6 的 Usage 字段，影响很小。

---

## 二、对照 §3 的缺口（尚未实现）

1. §3.1 分享者开通（协议、实名、支付宝加密存储）、注销结清；`mp_sellers` 从来没有写入（M9）。
2. §3.5 冷启动的「官方自营」挂单；自动暂停后通知分享者（代码里只有 TODO）；自买自卖排除在统计之外（M6）；验真时核对模型身份和余额大于 0（`verify.go:119` 只要求 key 有效且模型列表非空，没检查挂单声明的模型在不在返回列表里）；供应商流量走独立出站 IP，以及连续 3 次失败回落主 IP 的切换逻辑。
3. §3.6 优选、拉黑、只用某一家、strategy 都没生效（H6）；花费上限没做（H7）；单请求上下文上限没做；按买家计的 RPM（M13）。
4. §3.4 实际按次计费链路是断的（M4）；按次价目没有后台管理接口。
5. §3.7 首字节超时语义（M1）；三步结算不在同一事务里（H3）。
6. §3.10 embeddings、Gemini generateContent、`/v1/messages/count_tokens` 都不走市场：`isMarketplaceRelayPath` 只覆盖 chat、completions、responses、messages。错误码没有收敛（M8）。
7. §3.11 按真实客户端 IP 限速（100 次/秒，校验 CF-Connecting-IP / XFF 来源）、WAF 和高防、前端 5 秒缓存，代码里都没有。
8. §10 的待定项（402/403、model@分享者、无货模型的展示方式、按模型覆盖倍率）还等产品负责人确认。阶段 7 的真实 key E2E 和阶段 8 的前端还没做。

## 三、做得好的地方

1. **金额计算基础扎实**：`mulDivCeil` 用 128 位中间值，`Quote` 用 big.Int 对每个组成部分只向上取整一次，`SplitPlatformFee` 保证 fee+credit==debit。账本的 `(request_id, entry_type)` 唯一索引，加上「同值重放幂等、异值报冲突」，并且在 MySQL 和 PG 上都实测过，这部分质量很高。
2. **出网防护设计正确**：fail-closed 白名单，每次拨号只解析一次、校验全部 IP，然后直接用校验过的 IP 拨号，不走代理，没有 DNS rebinding 的 TOCTOU 窗口。
3. **提现的并发控制按规范实现**：事务内锁住卖家行再重算余额；审批、拒绝、打款都用 `UPDATE … WHERE status=` 并检查影响行数；有 50 goroutine 并发测试。
4. **接缝侵入小，单向依赖**：上游只改了约 91 行，marketplace 包不 import service、controller，靠函数注入避免了循环依赖。MP 关闭时与上游行为一致。
5. **key 处理规范**：AES-GCM 随机 nonce，渠道表里只存占位符，错误和日志里不出现 key；越权访问一律返回 404，不泄露资源是否存在；时段判定采用半开区间，节假日和调休的优先级正确，边界秒都有测试。
