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
