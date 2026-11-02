# E2E 巡检 Bug 记录与处理报告 (mp-c)

**巡检角色**: 管理员 + 前台页面巡检  
**测试端口**: 本机网关 3103，假上游 3203  
**分支**: `mp-c` (工作树: `D:\llmjar\wt-c`)  

---

## 1. 已修复并覆盖测试的 Bug

### Bug 1: 管理员模型池接口解析丢失 `enable_plaza` 字段导致广场开关状态错乱
- **严重程度**: High
- **复现步骤**:
  1. 管理员登录后访问 `/console/ops/models`；
  2. 后端接口 `GET /api/admin/pool/models` 返回 `{"data":[{"id":1,"name":"deepseek-flash","enable_plaza":false,...}]}`；
  3. 观察页面模型列表中的“广场”开关状态。
- **预期**: 开关为未选中状态（灰），提示“点击上架到广场”。
- **实际**: `site/src/features/admin/api.ts` 的 `normalizeAdminPoolModel` 未提取 `enable_plaza`，返回对象中该字段为 `undefined`，组件将其默认当成已上架 (`checked={true}`)，显示“点击下架广场”。
- **修复方案**: 在 `normalizeAdminPoolModel` 中提取 `enable_plaza` / `EnablePlaza` 并规范化为布尔值。
- **回归测试**: 在 `site/src/features/admin/pool.test.ts` 中补充 `enable_plaza: false` 字段规范化单测。

### Bug 2: 从新版后台跳转 `/ops-legacy` 原生后台 401 未认证
- **严重程度**: Medium
- **复现步骤**:
  1. 登录管理员账号；
  2. 点击侧边栏原生管理或 `/console/ops/models` 顶部的“前往 new-api 原生渠道管理页”进入 `/ops-legacy`；
  3. 浏览器直接发起原生 GET 请求。
- **预期**: 用户无缝进入原生后台渠道管理页面，无需再次重新输入密码登录。
- **实际**: 浏览器直接导航不携带 Authorization Header，`router/ops_legacy.go` 直接响应 401 Unauthorized。
- **修复方案**:
  1. 前端侧边栏及跳转按钮动态携带 `?token=${accessToken}`；
  2. 后端 `router/ops_legacy.go` 中解析 Query 中的 `token` 并转入 Header；对未带 token 的直接 HTML 访问输出微型 trampoline 脚本，自动从 `localStorage.getItem("token-market.auth.v1")` 提取 accessToken 进行跳转。
- **回归测试**: 通过 Playwright 自动化验证通过 `/ops-legacy?token=...` 进入后渠道列表成功拉取、渠道创建抽屉可正常打开。

### Bug 3: 文案混淆“模型广场”与“模型市场”术语
- **严重程度**: Low
- **复现步骤**:
  1. 访问模型市场详情页 `/models/:id`；
  2. 访问卖家入驻规则页面。
- **预期**: 统一将买家浏览渠道的二级市场称为“模型市场”（`/models`），平台官方直测区域称为“模型广场”（`/plaza`）。
- **实际**: `/models/:id` 空状态返回按钮为“返回模型广场”，卖家规则出现“未通过验真的渠道不会出现在模型广场”。
- **修复方案**: 在 `site/src/copy.ts` 中修正为“返回模型市场”，并校正卖家入驻相关文案。
- **回归测试**: 前端 Vitest 完整测试套件通过。

---

## 2. 纯产品决策/设计类问题（记录不改）

### Issue 4: 原生后台 `/ops-legacy` 在移动端 (390px) 存在水平滚动
- **严重程度**: Suggestion
- **实际表现**: 原生后台基于桌面 Semantic UI 框架构建，在 390px 视口下需要横向滚动操作。
- **原因与决策**: `/ops-legacy` 为仅限 Root 超级管理员在 PC 端进行底层渠道、日志等运维的老系统，非普通用户和移动端访问场景，保持原生兼容，不做重度 UI 重构。

### Issue 5: 模型多别名（10+ 标签）在小屏幕下的多行折行
- **严重程度**: Suggestion
- **实际表现**: 当后台为模型配置超过 10 个别名时，在小屏幕卡片展开时标签换行占用较大高度。
- **原因与决策**: 符合当前 Flex Wrap 设计规范，便于完整查看所有别名映射，无需硬编码截断。
