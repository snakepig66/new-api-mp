# 词元市场 · 前端重做方案（彻底脱离 new-api 前端）

版本：2026-10-24 · 配合 MARKETPLACE-DEV.md 使用

## 0. 结论
- 不在 `web/`（new-api 模板）上改。新建独立前端 `site/`，自己的设计系统、自己的路由、自己的文案，构建产物替换 Go 二进制内嵌的静态文件。
- new-api 只剩“后端引擎”：登录/令牌/充值这些接口继续调用，但界面上不再出现任何 New API、One API、QuantumNous、newapi.pro 字样。
- 上游管理后台不对外：迁到 `/ops-legacy`，只给超管用来配渠道和系统设置，第二版用自研后台完全替换。
- 中文优先，英文第二版再做。

## 1. 现状问题（线上实测）
- 全英文；首页是通用的 “Unified API Gateway” 营销模板。
- 模型广场 0 个模型，空状态却写着“没有符合筛选的模型”；分组 default/vip 这类内部概念直接暴露。
- 关于页显示 “No About Content Set” 和上游版权信息；Docs 链到 docs.newapi.pro。
- 首页和登录页用两套 logo，货币符号显示 ¤，页脚版权重复两次。
- 登录页只有一个光秃秃的表单，没有 GitHub 登录。
- 买家、分享者、钱包、挂单、提现这些市场功能全部没有界面，只在 /api/marketplace/* 里。

## 2. 技术选型
| 项 | 选择 | 理由 |
|---|---|---|
| 框架 | React 19 + TypeScript + Vite | 团队已在用 React 生态，构建快 |
| 路由 | TanStack Router（文件路由） | 类型安全，按模块拆包 |
| 数据 | TanStack Query + 自写 `api.ts`（fetch 封装） | 统一错误码、401 跳登录 |
| 样式 | Tailwind v4 + 自建组件（Radix 原语） | 不用 shadcn 默认皮肤，避免模板感 |
| 图表 | ECharts（按需引入）或 visx | 收益、用量趋势 |
| 表单 | react-hook-form + zod | 挂单向导、提现 |
| 金额 | 前端只做展示：后端返回整数微元，`formatYuan(micro)` 统一格式化 | 禁止前端算钱 |
| i18n | 先只做 zh-CN，文案集中在 `copy.ts` | 第二版再加 en |
| 测试 | Vitest + Playwright（关键路径 E2E） | |

目录：
```
site/
  src/
    app/        路由（/、/models、/models/$id、/docs、/login、/console/*、/seller/*、/admin/*）
    ui/         设计系统组件（Button、Input、Table、Tag、Price、Multiplier、Stat、Empty、Dialog、Toast）
    features/   market、keys、records、wallet、listings、payouts、admin
    lib/        api.ts、money.ts、time.ts（Asia/Shanghai）、auth.ts
    copy.ts     全部中文文案
  public/       logo、favicon、og 图
```

## 3. 视觉方向
- 浅色为主、信息密度高，参考 DeepSeek 开放平台 + OpenRouter 模型表的克制感，不用渐变大标题和营销插画。
- 主色：按钮 #087266，品牌 / logo #0E9F8E；中性灰阶；成功绿、警告橙、错误红各一个。
- 字体：系统中文字体栈（PingFang SC / HarmonyOS Sans / Microsoft YaHei），数字用等宽字体（JetBrains Mono），价格列右对齐。
- 统一组件：
  - `Price`：¥0.42 / 百万 tokens，峰谷两档时显示当前档高亮 + 另一档灰色。
  - `Multiplier`：0.30x，旁边显示“省 70%”。
  - `PeakBadge`：「高峰」橙 /「空闲」绿 + 倒计时到下次切换。
  - `SellerId`：分享者 #10023；`BuyerCode`：买家 7F3A9C21。
  - `Empty`：必须写清楚原因与下一步（例如“这个模型暂时没人挂单，挂一个？”）。
- 一套 logo、一个产品名，全站统一；页脚只写公司主体、ICP 备案号、用户协议、隐私政策。

## 4. 信息架构与页面

### 4.1 公开区（未登录可看）
1. **首页 /**：一句话价值（“同一个 Key，用官方模型更便宜”）+ 实时数据条（在售模型数、最低倍率、今日调用量）+ 热门模型价格表前 8 行 + 三步上手（注册 → 拿 Key → 粘贴 curl）+ 成为分享者入口。
2. **模型广场 /models**：高密度表格。列：模型、厂商、官方价（输入 / 输出）、最低倍率、到手价、在售分享者数、可用率、延迟 P50、当前时段。左侧筛选：厂商、能力（推理 / 视觉 / 工具 / 搜索）、只看有货。无货模型按 §10 决策隐藏或灰显。数据源 `GET /api/marketplace/square/models`。
3. **模型详情 /models/$id**：官方价目（峰谷两档 + 下次切换时间）、挂单列表（分享者编号、倍率、可用率、延迟、并发余量）、代码示例（OpenAI / Anthropic / curl 三个 tab，自动填 base_url 和模型名）、「加到我的 Key」按钮。数据源 `GET /square/models/:model`。
4. **文档 /docs**：一页式，左侧目录，每节一段可直接粘贴的 curl；错误码表；计费规则（峰谷、按次、失败不收费）。站内写，不外链。
5. **登录 /login、注册 /register**：邮箱 + GitHub，Turnstile；左侧品牌说明，右侧卡片表单。

### 4.2 使用者控制台 /console
1. **概览**：余额、本月花费、本月节省（对比官方价）、近 7 天用量图、最近 10 条请求。
2. **API Key**：列表（名称、已加模型数、花费 / 上限、策略、状态）；新建 Key 向导：命名 → 勾选模型并设每模型价格上限 → 策略（最便宜 / 均衡 / 最稳）→ 花费上限；Key 详情页可编辑优选 / 拉黑 / 只用某家 / 允许回落。数据源：上游 `/api/token` + `/api/marketplace/key/pref`、`/key/models`。
3. **使用记录**：表格（时间、模型、分享者、时段、tokens、搜索次数、官方价、实付、省了），点开看重试链和明细。数据源 `/records/usage`。
4. **钱包**：余额、充值（支付宝）、兑换码、流水。顶部醒目写“充值余额不可退”。

### 4.3 分享者控制台 /seller（同一账号顶部切换身份）
1. **开通引导**：协议 → 实名 → 绑定支付宝，进度条，完成前其他页只读。
2. **概览**：待结算（T+7）、可提现、本月收入、平台费、近 30 天收入图、挂单健康度。
3. **我的挂单**：卡片 / 表格，状态标签（验真中 / 灰度 / 在售 / 暂停 / 封禁）+ 暂停原因（余额不足、key 失效、手动）；行内改倍率（显示 10 分钟冷却倒计时）、暂停 / 恢复、换 key。
4. **挂单向导（4 步）**：选厂商 → 粘贴 key（只显示末 4 位）→ 自动检测结果（有效性、余额、可用模型、延迟、缓存支持），勾选上架模型 → 定倍率（实时预览买家价、你的到手价、与同模型最低价对比）+ 并发 / RPM / 日限额 / 接单时段。
5. **分享记录**：时间、模型、买家代号、tokens、收入、平台费、状态（待结算 / 可提现 / 已冲正）。
6. **提现**：可提现金额、最低 ¥50、本周是否已提、历史记录与状态。

### 4.4 运营后台 /admin（自研，替换上游后台中的市场部分）
价目表与版本、节假日日历导入、挂单审核与封禁、提现审批（批准 / 拒绝 / 标记已打款）、冲正、代号反查、按次计费项。上游渠道和系统设置第一版暂留 `/ops-legacy`。

## 5. 后端需补的接口（前端开工前要有）
| 需要 | 现状 | 建议 |
|---|---|---|
| 当前用户 + 身份（是否已开通分享者、开通进度） | 无 | `GET /api/marketplace/me` |
| 分享者开通：签协议、实名、绑支付宝 | 无 | `POST /seller/onboarding/*` |
| 挂单检测（不落库，先检测再提交） | 只有创建接口 | `POST /seller/listings/probe` |
| 挂单详情 + 探测历史 + 暂停原因 | 只有列表 | `GET /seller/listings/:id` |
| 分享者收益趋势 / 买家用量趋势 | 无 | `GET /seller/stats`、`GET /records/usage/stats`（按日聚合） |
| 提现历史 | 只有申请 | `GET /seller/payouts` |
| 买家余额、流水、本月节省 | 只有上游余额 | `GET /wallet`、`GET /wallet/ledger` |
| 首页实时数据 | 无 | `GET /api/marketplace/stats/public`（缓存 60s） |
| 统一错误格式 + 中文 message | 部分 | 前端按 code 映射文案 |
| GitHub OAuth 回调落到新前端 | 上游跳旧页面 | 回调后跳 `/console` |

## 6. 与 Go 后端的集成
- 构建：`site/` 下 `bun run build` 产出 `site/dist`；`main.go` 的 embed 从 `web/dist` 改成 `site/dist`（一处 MP-HOOK）。
- 路由：所有非 `/api`、`/v1` 的路径返回 `site/dist/index.html`（SPA fallback）；`/ops-legacy/*` 挂上游旧前端（仅超管，可开关）。
- 上游 `web/` 不删，避免与上游合并冲突，但不再参与构建。
- 系统设置里的 SystemName、Logo、About、Footer、DocsLink 不再使用，新前端写死自己的品牌。

## 7. 排期（约 10–12 个工作日）
| 阶段 | 内容 | 天 |
|---|---|---|
| F0 | 脚手架、设计系统（10 个基础组件）、api.ts、金额 / 时间工具、embed 切换 | 1.5 |
| F1 | 公开区：首页、模型广场、模型详情、文档、登录注册 | 2.5 |
| F2 | 使用者控制台：概览、Key 向导与详情、使用记录、钱包 | 2.5 |
| F3 | 分享者：开通引导、挂单向导、我的挂单、分享记录、提现 | 2.5 |
| F4 | 运营后台市场部分 | 1.5 |
| F5 | 空状态 / 错误态 / 移动端适配、Playwright 关键路径、部署 llmjar | 1 |
后端补接口（§5）约 2 天，可与 F0–F1 并行。

## 8. 验收标准
- 全站搜索不到 New API / One API / QuantumNous / newapi.pro / ¤。
- 所有金额来自后端微元，前端无任何金额加减乘除（lint 规则禁止对 `*_micro` 字段做运算）。
- 每个列表都有加载态、空态（写原因和下一步）、错误态。
- 关键路径 E2E：注册 → 建 Key → 加模型 → 调用 → 看记录；开通分享者 → 挂单 → 改倍率 → 提现申请。
- 1440px 与 390px 宽度下无溢出；Lighthouse 性能 ≥ 90。
- 首屏 JS ≤ 200KB（gzip）。

## 9. 需要你定的
1. 产品名和 logo（现在全站没有自己的品牌）。
2. 无货模型隐藏还是灰显。
3. 上游旧后台是否保留 `/ops-legacy`，还是第一版就全部自研。
