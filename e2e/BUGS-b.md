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
