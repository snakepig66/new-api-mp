# E2E 测试缺陷记录 (Track B - 卖家全流程)

本文档记录在 D:\llmjar\wt-b（分支 mp-b）进行「卖家全流程」端到端测试过程中发现的缺陷与修复情况。

---

### BUG-001: 本地开发与测试模式下 MP_ALLOW_LOOPBACK_BASE_URL=1 未对出网防护（Egress Guard）放行回环地址

- **所属模块**: `marketplace/egress.go`
- **严重程度**: High (阻断本地端到端测试及以 localhost / 127.0.0.1 作为假上游的卖家渠道验真和转发)
- **复现步骤**:
  1. 设置环境变量 `MP_ALLOW_LOOPBACK_BASE_URL=1`，启动本地假上游服务（如 `http://127.0.0.1:3202`）。
  2. 卖家新建自定义渠道并填入 `http://127.0.0.1:3202`，触发连通性检测（probe）或提交挂单后由后台异步验真。
  3. `EgressVerifyHTTPClient` / `RelayHTTPClient` 执行 dial 校验。
- **预期结果**:
  当显式配置 `MP_ALLOW_LOOPBACK_BASE_URL=1` 时，`CheckEgressHost` 与 `ValidateEgressIP` 应放行 loopback 地址（`127.0.0.1`、`::1`、`localhost`），允许向本地上游拨号。
- **实际结果**:
  `ValidateEgressIP` 无条件检查 `addr.IsLoopback()` 并拒绝（报错 `marketplace: egress denied: non-public ip 127.0.0.1`）；`CheckEgressHost` 亦无条件拒绝 IP 字面量（报错 `ip literal "127.0.0.1" is not a whitelisted domain`）。
- **修复方案**:
  在 `marketplace/egress.go` 中，当 `os.Getenv(MPAllowLoopbackBaseURLEnv) == "1"` 时，允许 `localhost` 及 loopback IP 通过 `CheckEgressHost` 与 `ValidateEgressIP`。未配置该环境变量时保持原有的生产级严格拒绝。

---

### BUG-002: 前端渠道向导 validCustomURL 限制回环地址不能使用 http 协议

- **所属模块**: `site/src/features/seller/wizard.ts`
- **严重程度**: Medium (向导前端校验阻断输入 http 回环地址)
- **复现步骤**:
  1. 打开「我要卖 → 我的渠道 → 新建渠道」。
  2. 在接口地址输入 `http://127.0.0.1:3202` 或 `http://localhost:3202`。
- **预期结果**:
  与后端 `custom_channel.go` 的规范保持一致：常规自定义渠道强制 `https:`，但针对回环地址（`localhost`、`127.0.0.1`、`::1`）放行 `http:`，允许卖家输入并进入后续检测与配置。
- **实际结果**:
  前端 `validCustomURL` 强制匹配 `^https:\/\/` 且 `parsed.protocol === 'https:'`，将 `http://127.0.0.1:3202` 判定为无效地址，禁止下一步及自动检测。
- **修复方案**:
  更新 `site/src/features/seller/wizard.ts` 中的 `validCustomURL`，允许回环地址使用 `http:` 协议，其他外部域名继续强制 `https:`。补齐相应单元测试。

---

### BUG-003: 429 余额/额度不足及超时错误未被简化为大白话文案（"上游余额不足" / "地址连不上"）

- **所属模块**: `marketplace/model_health.go`, `marketplace/verify.go`, `marketplace/custom_channel.go`, `site/src/app/routes/console/listings/new.tsx`
- **严重程度**: Medium (用户体验缺陷，导致错误报错展示为原始技术信息或通用报错而非大白话)
- **复现步骤**:
  1. 上游返回 HTTP 429 且响应体为额度用尽（如 `{"error": {"message": "You exceeded your current quota...", "code": "insufficient_quota"}}`）或 HTTP 402。
  2. 触发探活检测，`model_health.go` 抛弃了上游响应体，直接按 429 状态码返回通用限流文案（"上游限流（HTTP 429），请稍后重新检测"），未能识别为"上游余额不足"。
  3. 当发生 `context deadline exceeded` 时，因未包含 "timeout" 字符串，未能简化为"地址连不上"。
- **预期结果**:
  - Key 无效 (401/403) 统一展示为 "Key 无效或没权限"；
  - 余额/额度不足 (402 或 429 包含 quota/insufficient/balance) 统一展示为 "上游余额不足"；
  - 地址超时/连不上（包含 connection refused, connectex, deadline exceeded, dns 等）统一展示为 "地址连不上"。
- **实际结果**:
  429 额度用尽被提示为"上游限流"，402 返回"模型探活失败（HTTP 402）"，deadline 错误原样展示。
- **修复方案**:
  1. 在 `marketplace/model_health.go` 中解析上游响应体消息，当 402 或 429 且包含 quota/insufficient/balance/余额等时归类为"上游余额不足"；
  2. 在 `marketplace/verify.go` 的 `modelCallFailureReason` 中对 402 显式返回"上游余额不足（HTTP 402）"，在 `SimplifyProbeFailureReason` 中增加 `deadline` 判定；
  3. 在 `site/src/app/routes/console/listings/new.tsx` 的 `simplifyProbeError` 中增加 `deadline` 判定并对 429 quota 转换为"上游余额不足"。补齐单元测试。

---

### BUG-004: marketplace/seller.go 违反项目规范直接调用 encoding/json

- **所属模块**: `marketplace/seller.go`
- **严重程度**: Low (违反 AGENTS.md 强制规范)
- **复现步骤**:
  代码审计 `marketplace/seller.go`，发现多处直接引用 `encoding/json` 的 `json.Marshal` 与 `json.Unmarshal`。
- **预期结果**:
  依照项目 `AGENTS.md` 规范，根模块内所有业务代码的 JSON 序列化与反序列化必须统一使用 `common.Marshal` / `common.Unmarshal`，不得直接调用 `encoding/json`。
- **实际结果**:
  `marketplace/seller.go` 引入了 `encoding/json` 并直接调用 `json.Marshal` / `json.Unmarshal` 处理收款账户信息与重试跳步信息。
- **修复方案**:
  替换为 `common.Marshal` 与 `common.Unmarshal`，移除 `encoding/json` 导入。

---

### BUG-005: 管理员提现审批打款确认弹窗中微信收款账号/收款码展示缺失

- **所属模块**: `site/src/app/routes/console/ops/review/payouts.tsx`
- **严重程度**: Low (界面信息不一致)
- **复现步骤**:
  1. 卖家绑定微信收款方式（填写微信手机号并上传收款码），发起提现。
  2. 管理员进入「运营后台 → 审核与审批 → 提现审批」，点击「批准」或「标记已打款」。
  3. 查看确认弹窗中的「收款方式」信息。
- **预期结果**:
  弹窗中应完整展示收款人姓名及微信绑定的手机号/收款码标识（与支付宝展示姓名 + 手机号保持一致体验）。
- **实际结果**:
  微信渠道仅展示了 `微信支付 (姓名)`，丢失了手机号和收款码信息，导致打款确认时无法核对打款手机号。
- **修复方案**:
  在打款确认弹窗中为微信支付补充账号手机号与收款码提示。
