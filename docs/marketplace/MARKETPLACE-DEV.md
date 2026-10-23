# LLM API 交易市场 · 开发文档（全新 new-api 重做版）

版本：2026-10-23 · 适用：本地 coding agent 从最新上游 new-api 重新实现
经营主体：福建省宁德市词元科技网络有限公司

本文件是唯一需求来源。遇到本文没写或互相矛盾的地方，先问产品负责人，不要自行决定。

---

## 1. 产品一句话

分享者把自己的国内大模型官方 API key 挂到平台上，按「官方价 × 倍率」出售；使用者用一个平台 Key、一个 OpenAI 兼容端点调用所有模型，平台自动选最合适的分享者转发，按实际用量计费，平台从分享者收入中抽 7%。

---

## 2. 硬性约束（每次提交前自查）

1. 金额一律整数**微元**：1 元 = 1,000,000 微元。全程禁止浮点记账。new-api 原有 quota 只作内部换算，界面上不出现。
2. 新逻辑全部放在独立包 `marketplace/`；上游代码只在少数接缝处改动，每处用 `// MP-HOOK` 注释标记，单处改动尽量控制在几行内。
3. 不在日志、提交、错误信息、接口返回里输出任何 key 或密钥。
4. 数据库用 GORM，必须兼容 SQLite / MySQL / PostgreSQL（遵守仓库 AGENTS.md）。
5. 一个独立事项一个 commit；每完成一项，更新 `docs/marketplace/PROGRESS.md`（做了什么、取舍、测试结果）。
6. 收尾必跑：`go build ./...`、`go vet ./marketplace/...`、`go test ./marketplace/... -race -count=1`。
7. 不 push 到上游 QuantumNous/new-api。

---

## 3. 已定的产品规则

### 3.1 账号与身份
- 一个账号，可在「使用者 / 分享者」两种身份间切换；分享者需额外开通：签协议 → 实名 → 绑定支付宝收款账户。
- 登录：邮箱 + GitHub。登录、注册接 Turnstile。
- 用户编号：自增数字 ID，**从 10011 开始**（users 表自增起点）。公开页显示「分享者 #10023」。
- 隐私：买家能看到分享者编号；分享者只看到买家的匿名代号 = `HMAC(secret, seller_id || buyer_id)` 截 8 位（每个分享者看到的代号不同）。后台可反查完整对应关系。
- 不保存买家的对话内容。

### 3.2 定价
- 平台为每个模型维护**官方价目表**（¥/百万 tokens：输入、输出、缓存读、缓存写）。
- 分享者给每条挂单设一个**倍率**，范围 0.001–1.000，精度 0.001，存整数千分值 1–1000；界面直接写「0.3x」，不换算成几折。
- 买家单价 = 当时时段官方价 × 倍率。买家看到的就是最终价。
- 平台费 = round(买家扣款 × 7%)，分享者收入 = 扣款 − 平台费（分享者到手约 93%）。
- 买家扣款按微元向上取整。
- 推理 tokens 按输出价；缓存读、缓存写用各自价格。
- 界面文案：倍率写「0.3x」，金额写「省了 / 赚了多少」，不出现内部术语。

### 3.3 官方分时价（第一版必须做）
- DeepSeek 官方峰谷：北京时间周一至周五 09:00–12:00、14:00–18:00 为高峰，价格为空闲时段 2 倍；周末、中国法定节假日全天空闲价；调休上班的周末按工作日规则。
- 价目表结构：每个模型可配多个时段档（peak / offpeak），每档 4 个价；不分时的厂商只配一档。价目表带版本号。
- 时段判定统一用 `Asia/Shanghai`；节假日 / 调休日历存表，后台可改，按年导入。
- 计价时刻 = 预冻结那一刻；整条请求（含重试链）用同一时段和同一价目版本，不跨档拆分。
- 倍率不分时段。
- 种子数据（可配置，不写死在逻辑里）：`deepseek-flash`、`deepseek-v4-pro` 的高峰 / 空闲价，以 https://api-docs.deepseek.com/zh-cn/quick_start/pricing 为准。

### 3.4 Search / Fetch 按次计费（第一版必须做）
- 自北京时间 **2026-11-01 00:00** 起，DeepSeek 服务端 Search / Fetch 每 1000 次成功请求 ¥5，即 **¥0.005/次 = 5,000 微元/次**；失败、无结果、超时不收。Claude Code、Codex 等走 DeepSeek API 时服务端会自动调用。
- 从上游 usage 读取次数（如 Anthropic 格式 `usage.server_tool_use.web_search_requests` 及 fetch 次数），按官方单价 × 倍率计费，单独一行流水，同样 7% / 93% 分成。
- 读不到次数时**不能估算收费**：该厂商的服务端搜索类工具默认关闭——在入站原始 JSON（格式转换之前）剥离或拒绝该工具，直到能读到次数为止。
- 预冻结额外按每请求最多 N 次（默认 10，可配）冻结。

### 3.5 货源与挂单
- 首批只接**国内厂商官方渠道**（之后会开放其他货源，数据结构要留余地）。
- 供应商白名单，base_url 由平台固定，分享者只提交 key，不能填地址。
- 一条挂单 = 一把 key = 一个上游渠道。key 加密存储，另存 HMAC 指纹，同一把 key 全平台只能挂一次。任何接口不返回 key。
- 挂单可设：并发上限（必填，默认 5）、RPM 上限、每日金额上限、可接单时段（可选）。并发槽在请求结束或超时（10 分钟）释放；满并发跳过这家，不排队。
- 状态机：draft → verifying → canary → live ⇄ paused → banned。
- 挂单向导：key + 可用模型一次提交、一次检测；检测内容为 key 有效、余额、模型列表、延迟，并自动探测是否支持 prompt 缓存，展示给分享者。
- 改倍率：不重新验真；两次修改至少间隔 10 分钟；只对新请求生效。换 key：必须重新验真。
- 上游报余额不足或 key 失效：立即自动暂停并通知分享者。429：只熔断这条挂单 60 秒并换家，之后逐步放量；429 不算额度耗尽。
- 验真 / 巡检请求走独立限流桶与并发，不占买家流量的并发和 RPM。
- canary 灰度默认 10% 流量，24 小时内成功率 ≥98% 自动转 live。
- 自买自卖允许，但不计入排名和可用率统计。
- 冷启动：平台用自己的 key 挂一个「官方自营」分享者，倍率 1.0，作兜底。
- 分享者注销：先结清冻结中和可提现余额，再停用全部挂单。

### 3.6 使用者与 Key
- 买家 Key 可设：花费上限、价格上限（按倍率）、策略（cheapest / balanced / stable）、优选名单、拉黑名单、「只用某一家」。
- 模型由买家自己加到 Key 上：创建 Key 时勾选，或在模型详情页「加到我的 Key」，并可设该模型的价格上限。调用未添加的模型时报错，提示去添加。（已倾向，需在 §10 确认后实现）
- 优选全部不可用时自动回落全量；「只用某一家」不可用时默认直接报错，可改为允许回落。兜底换家同样受价格上限约束。
- 每个买家有自己的 RPM 限制。
- 公平使用：单请求上下文上限可由平台按模型配置（默认不限）。

### 3.7 计费与失败处理
- 用量以上游 usage 为准；流式请求强制带 `stream_options.include_usage`；上游没返回时用估算并标「估算」。
- 预冻结 = 输入估算 + max_tokens × 输出价（+ Search/Fetch 上限）；没传 max_tokens 时按模型最大输出估算，但不超过余额；余额不足报错。
- 失败不扣费。换一家就重新计价、重新冻结，最多换 2 家；全部失败 ¥0。
- 只剩一家且是瞬时失败（超时、5xx）：原地重试 1 次，再报「暂无可用供应商」。
- 首字节超时 20 秒，超时换下一家（计入最多 2 次重试）。
- 截断的流、空响应、上游正常结束但 0 输出 token：判失败，不扣费；chat、responses、messages 三条路径都要保护。
- 长文本 token 预估：超过 64KB 分段采样估算，禁止转发前同步全量分词。
- 展示价、扣费、记录详情三处共用**同一个算价函数**。

### 3.8 记录
- 一个 request_id 只生成一条记录；换家、熔断事件折叠进这条记录的重试链。
- 使用记录（买家）与分享记录（分享者）用同一个 request_id 一一对应。
- 记录字段：模型、分享者编号 / 买家代号、计价时段（高峰 / 空闲）、tokens 明细、缓存命中、搜索 / 抓取次数及费用、官方价、实付、省了多少（分享者侧：收入、平台费）。

### 3.9 资金
- 流水：复式记账，`buyer_debit = seller_credit + platform_fee`，同一 request_id 合计为 0；`(request_id, entry_type)` 唯一，保证幂等。
- 分享者收入先 pending，`available_at = now + 7 天`（T+7），到期由定时任务转 available。
- 冻结期内发现掺假、盗刷、拒付可冲正（reversal）。
- 买家充值余额不退（用户协议 + 充值页醒目提示）。
- 收款先接支付宝（支付与合规由产品负责人处理，开发侧只留接口）。
- 提现：人工审核；最低 ¥50；每周一次；支付宝手续费平台承担。
- 拉新：用 new-api 自带兑换码批量生成体验金码（按金额）；体验金不可提现。

### 3.10 对外接口
- 同一个 Key 支持：OpenAI chat / responses / embeddings、Anthropic `/v1/messages` 与 `/v1/messages/count_tokens`、Gemini generateContent。
- 对外错误码只暴露：401 无效 Key、余额不足（402 或 403，待定）、400 参数错误、503 暂无可用供应商；统一 OpenAI 错误格式。
- 文档：一页式，每节一段可直接粘贴的 curl。

### 3.11 安全
- 出网只允许白名单域名；每次拨号都校验解析出的 IP，拒绝私网、回环、链路本地（防 SSRF 与 DNS rebinding）；上游错误不原样回显给分享者。
- 提现：事务内锁用户行、重算可提余额再扣；审批和拒绝都用 `UPDATE … WHERE status='pending'`，影响行数为 1 才继续。
- 入口放 WAF / 高防，源站只接内网来源；按真实客户端 IP 限速（默认 100 次/秒，取 CF-Connecting-IP / XFF 并校验来源）；前端页面 5 秒缓存。
- 供应商流量走独立出站 IP；连续 3 次探测不通回落主 IP，恢复后等 10 分钟再切回。

---

## 4. 架构

### 4.1 包结构建议
```
marketplace/
  models.go        数据模型 + AutoMigrate
  money.go         微元工具、取整、分成
  pricebook.go     价目表、版本、按次计费项
  timeslot.go      Asia/Shanghai 时段判定 + 节假日日历
  quote.go         唯一算价入口 Quote(...)
  ledger.go        复式流水、T+7、冲正
  billing.go       预冻结 / 结算会话
  selector.go      选渠道、打分、并发槽、熔断
  egress.go        出网白名单 + 拨号校验
  vendors.go       供应商白名单（可配置）
  listing.go       挂单、验真、状态机
  keyprefs.go      买家 Key 偏好与模型
  servertools.go   Search/Fetch 检测、剥离、计次
  payout.go        提现
  jobs.go          定时任务（T+7、canary 升级、巡检）
  api_buyer.go / api_seller.go / api_admin.go
  *_test.go
```

### 4.2 数据表
| 表 | 要点 |
|---|---|
| mp_sellers | user_id、状态、实名状态、协议版本、收款账户（加密）、信用分 |
| mp_price_books | model、vendor、tier（peak/offpeak/flat）、input/output/cache_read/cache_write（微元/百万 tokens）、version、effective_at |
| mp_per_call_prices | vendor、item（search/fetch）、单价微元/次、version、effective_at |
| mp_holidays | date、kind（holiday / workday 调休上班） |
| mp_listings | seller_id、channel_id、vendor、models、multiplier_milli、concurrency、rpm、daily_cap_micro、hours、status、key_fingerprint、version、multiplier_updated_at |
| mp_probes | listing_id、类型（验真/巡检/举报）、结果、延迟、时间 |
| mp_key_prefs | token_id、策略、价格上限、优选、拉黑、仅限某家、允许回落 |
| mp_key_models | token_id、model、price_cap_milli |
| mp_requests | request_id、buyer、listing、model、时段、价目版本、倍率、tokens 明细、搜索/抓取次数、状态、重试链（JSON） |
| mp_ledger | request_id、entry_type（buyer_debit / seller_credit / platform_fee / refund / reversal / tool_debit…）、account、amount_micro、listing_id、price_version、multiplier_milli、status（pending/available/reversed）、available_at；`(request_id, entry_type)` 唯一 |
| mp_payouts | seller_id、金额、状态（pending/approved/paid/rejected）、幂等键、审核人、时间 |

### 4.3 请求链路
1. 买家带 Key 调 /v1 → 校验模型已加到 Key。
2. 取该模型 live（+ canary 灰度）挂单 → 按 Key 偏好、价格上限、拉黑 / 优选、并发槽、熔断状态过滤 → 综合价格、可用率、延迟打分选一家。
3. 用 `Quote(listing, model, now)` 按当前时段与价目版本预冻结。
4. 转发：首字节 20s；失败按 §3.7 换家，换家时重新 Quote（时刻仍用首次预冻结时刻）、重新冻结。
5. 成功：在一个事务内按实际 usage 写流水（token 费 + 按次费），释放剩余冻结，写 mp_requests 一条记录。
6. 全部失败：释放冻结，¥0，记录标失败并附重试链。

### 4.4 上游接缝（只改这些地方，全部标 `// MP-HOOK`）
| 位置 | 作用 |
|---|---|
| middleware/distributor.go | 市场请求走 marketplace 选渠道 |
| controller/relay.go（getChannel / 重试循环） | 换家、重新计价、原地重试 |
| service/relay_error.go | 429 熔断、余额不足 / key 失效暂停 |
| relay/channel/api_request.go | 出网拨号校验、首字节超时 |
| relay/request_billing.go | 预冻结 / 结算会话（实现上游 BillingSettler 接口） |
| service/text_quota.go | 结算接入市场流水 |
| router/api-router.go | 注册市场控制台 API |
| main.go | 初始化、迁移、定时任务 |

上游最新版本若接口位置变化，按同等职责找新位置，并在 PROGRESS.md 记下。

### 4.5 环境变量
`MP_ENABLED`、`MP_KEY_SECRET`（必须设置并备份，丢失则所有分享者 key 无法解密）、`QUOTA_PER_UNIT=1000000`、`RetryTimes>=2`、`MP_EGRESS_DOMAINS`（可选，覆盖白名单）。

---

## 5. 开发计划

合计约 12–14 个工作日（不含前端）。每段完成且验收通过再进下一段。

### 阶段 0 · 起步（0.5 天）
- clone 上游最新 main，记下基线 commit，建分支 `mp`。
- 读 AGENTS.md；`web` 下 `bun run build`（`go build` 需要 `web/dist`）；SQLite 跑起来能登录后台。
- 建 `docs/marketplace/`，放本文件与 PROGRESS.md。
- 验收：`go build ./...` 通过；`marketplace` 包被 main 引入且 `MP_ENABLED=false` 时对上游零影响。

### 阶段 1 · 数据与钱（2 天）
- 建表与迁移（三方言），users 自增起点 10011，匿名代号函数。
- money.go：微元、向上取整、7% 四舍五入分成。
- ledger.go：复式写入、幂等、T+7 转正、冲正。
- 验收：恒等式、幂等重放、取整边界、冲正单测全部通过。

### 阶段 2 · 价目表与统一算价（1.5 天）
- pricebook / timeslot / quote。
- 节假日日历表 + 年度导入接口。
- 种子：DeepSeek 两模型峰谷价、search/fetch 单价（2026-11-01 00:00 CST 生效）。
- 验收测试：11:59:59 / 12:00:00、13:59:59 / 14:00:00、17:59:59 / 18:00:00、午休、周末、法定节假日、调休上班周末、跨档请求按冻结时刻计价、价目版本切换。

### 阶段 3 · 分享者与挂单（2 天）
- key 加密 + 指纹唯一；供应商白名单；egress 拨号校验。
- 挂单 CRUD、状态机、改倍率 10 分钟间隔、换 key 重验真。
- 验真（独立限流桶）：有效性、余额、模型列表、延迟、缓存支持。
- 验收：SSRF / DNS rebinding 用例、重复 key 拒绝、状态流转、限流隔离单测。

### 阶段 4 · 请求链路（3 天，核心）
- 按 §4.4 接缝接入。
- selector：过滤、打分、并发槽、429 熔断 60s、自动暂停。
- 重试：最多换 2 家、重新计价、单家原地重试 1 次、首字节 20s。
- 失败保护：截断流 / 空响应 / 0 输出 token 不扣费（三条协议路径）。
- 流式强制 include_usage；>64KB 采样估 token。
- servertools：入站 JSON 检测与剥离、usage 读次数、按次流水、预冻结 +10 次。
- 一条 request 一条记录 + 重试链。
- 验收：用 mock 上游做集成测试，覆盖成功、换家、熔断、截断、0 token、全失败、搜索计次、读不到次数时剥离；每个用例后买家余额、冻结、流水三方对平。

### 阶段 5 · 买家侧（1.5 天）
- Key 偏好、加模型、价格上限、策略、只用某家、买家 RPM。
- 错误码收敛到 §3.10。
- 使用记录 / 分享记录 API（按 request_id 对应），模型广场 API 返回「当前高峰 / 空闲、两档价格、下次切换时间」。
- 验收：接口测试 + 权限测试（分享者拿不到买家真实身份、买家拿不到 key）。

### 阶段 6 · 提现与后台（1.5 天）
- 提现申请、审核、拒绝（条件更新）、幂等键、最低 ¥50、每周一次。
- 后台 API：价目表、节假日、挂单审核、提现审批、冲正、代号反查。
- 定时任务：T+7 转正、canary → live 升级、巡检。
- 验收：50 goroutine 并发提现测试余额不为负、不超额、拒绝不重复退款。

### 阶段 7 · 收尾验证（1–2 天）
- `go vet`、`-race` 全量测试。
- docker 起 MySQL 与 PostgreSQL，各跑迁移 + 全量测试。
- 真实 DeepSeek key 端到端：高峰一单、空闲一单、流式、Anthropic messages、带搜索请求，人工对账。
- 结果写入 PROGRESS.md。

### 阶段 8 · 前端（另排期）
- 使用者端：模型广场（OpenRouter 式高密度对比表）、模型详情、Key、使用记录、钱包。
- 分享者端：挂单四步向导、我的挂单、分享记录、收益与提现。
- 公开页；上游后台加挂单审核、提现审批。
- 风格：DeepSeek 式浅色 + 青绿主色（按钮 #087266，logo #0E9F8E）。

---

## 6. 测试清单（汇总）
- 金额：恒等式、幂等、向上取整、7% 四舍五入、微元级小额请求。
- 时段：所有边界秒、午休、周末、节假日、调休、跨档、版本切换。
- 链路：换家重计价、429 熔断、余额不足暂停、首字节超时、单家原地重试、全失败 ¥0、截断 / 空 / 0 token 不收费。
- 按次：读到次数计费与分成、读不到时剥离 / 拒绝、预冻结上限。
- 安全：SSRF、DNS rebinding、key 不回显、匿名代号不可逆、提现并发。
- 数据库：SQLite / MySQL / PostgreSQL 三方言。

---

## 7. 部署与测试服务器（llmjar）
- 服务器已清空：`/data/workspace` 为空；旧服务 new-api-mp 已停用并删除；保留 ZCode server、SSH、docker。
- 连接（instabox 网关只接受证书型主机密钥，普通 SSH 客户端走本地转发）：
  ```bash
  instabox ssh llmjar -- -N -L 2222:127.0.0.1:22   # 窗口保持
  ssh -p 2222 user@127.0.0.1                         # 私钥或自设密码
  rsync -avz -e 'ssh -p 2222' ./ user@127.0.0.1:/data/workspace/new-api/
  ```
  也可直接 `instabox ssh llmjar`。
- 服务器闲置 30 分钟自动挂起。
- 对外端口：服务监听 3000，经 instabox 公网端口暴露（https://3000-agqvtobwrbdo3hk4ztmazwjmei.instabox.tech）。
- 建议用 systemd 跑服务，环境变量放 `/etc/new-api.env`，`MP_KEY_SECRET` 单独备份。

---

## 8. 第二版（现在不做，但数据结构别堵死）
- 非官方货源：自定义供应商地址 + 保真等级 L0–L3 + 响应一致性抽检。
- 订阅 / OAuth 类凭证（合规另评估）。
- 分享者自定义峰谷倍率。
- 免费池与窗口限额（5 小时 + 周）。
- 新模型限时免费、Playground、海外模型、新管理后台。

---

## 9. 经验备忘
- 天翼云 eaichat 网关只有 `deepseek-flash` 是真 DeepSeek，其他 deepseek 写法会被静默换成 GLM——验真时要核对返回的模型身份。
- 旧 fork 审查出的高危问题，新实现必须从设计上避免：重试换家后计价不重算、提现拒绝重复退款、提现并发超额、允许任意上游地址导致 SSRF 回显。

---

## 10. 开工前需产品负责人确认
1. 流式中途断开：按已生成 usage 计费，还是截断不收费？（当前文档按「不收费」）
2. 改倍率是否回验真？（当前按「不回，10 分钟间隔」）
3. 余额不足返回 402 还是 403？
4. 首批供应商白名单与各家基准价（DeepSeek 之外：Kimi、智谱、千问、MiniMax 等）。
5. 是否允许按模型覆盖倍率；是否支持 `model@分享者编号` 单次指定；无货模型隐藏还是灰显。
6. 买家 Key 自己添加模型 + 每模型价格上限，按 §3.6 实现？
7. T+7 成熟期是否最终确定。
