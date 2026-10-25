# 模型池 / 渠道池实施设计（MODEL-CHANNEL-POOL）

> 依据：用户 2026-10-25 的《模型池 / 渠道池实施计划》。本文把计划落成可执行的契约与决策，
> 后续 P1–P6 均以此为准。与 MARKETPLACE-DEV.md 冲突时，以本文的池化设计为准。

## 0. 目标

把「写死的 vendor + 模型×渠道价目 + 挂单上游名」升级为：

- **标准模型池**（mp_models）：模型只有一份官方价、一组别名、一份验真题。
- **渠道池**（mp_channels）：官方 / 套餐 / 第三方三类渠道，URL 固定，key 规则可校验，
  套餐渠道单独记录额度窗口与耗尽特征。
- **可卖资格表**（mp_channel_models）：渠道 × 上游模型名 → 标准模型 + 价格系数 + 与官方版差异。

请求链路：**别名归一 → 选路（已认证/在售/未暂停/能力满足）→ 上游名映射 → 响应 model 改回
标准名 → 按标准模型官方价 × 系数 × 倍率计费**。

## 1. 三张表（契约）

见 `marketplace/modelpool.go`（P1）。要点：

- `mp_models`: name(unique), origin_vendor, context_length, capabilities(JSON), aliases(JSON),
  verify_prompt, status。
- `mp_channels`: slug(unique), name, kind(official/plan/third_party), protocols(JSON),
  allow_host_pattern, key_pattern(regex), deny_paths(JSON), deny_key_prefixes(JSON),
  adaptor, strip_v1_prefix, passthrough_body, quota_window(JSON), exhaustion_signature,
  terms_url, terms_checked_at, status。
- `mp_channel_models`: (channel_slug, upstream_model) unique, (channel_slug, model) unique,
  coefficient_milli(1000 = ×1.0), differences(JSON), verified, verified_at,
  fingerprint_ratio, status(active/paused/banned)。

旧表 `mp_channel_model_map` 迁移进 `mp_channel_models` 后保留只读，不再被代码引用。
价目 `mp_price_books` 的 vendor 语义改为**原厂**；同模型其它 vendor 的重复价目在迁移时清理
（价格完全一致才删，不一致保留并告警）。

## 2. 决策（对应计划末尾「需要你定」）

| 问题 | 决策 | 理由 |
| --- | --- | --- |
| 认证探针用的平台 key | 代码就绪，种子渠道标记 `verified=false`；平台 key 由运营通过管理接口/环境注入后跑探针 | 本仓库不含任何厂商 key |
| 指纹抽检费用 | 平台承担；默认每挂单每天 1 次，可由环境变量 `MP_FINGERPRINT_INTERVAL` 调整 | 计划已确认 |
| 与官方版差异大的套餐模型 | **单独标准名**（如 `glm-5.3-coding`），不混入官方标准名 | 避免买家以官方名买到能力缩水的模型 |
| `MP_EXTRA_VENDORS` | 仅 `MP_ENV=test` 时生效；生产忽略并记日志 | 计划要求 |
| 套餐 key 填错渠道 | `key_pattern` 不匹配直接拒绝；套餐渠道额度耗尽 → 暂停到窗口重置，不算故障 | 计划要求 |

## 3. 请求链路改造（P2）

1. **别名归一**：`ModelPoolResolveAlias(db, input)`，大小写不敏感匹配 Name 与 Aliases
   （`glm5.3` / `GLM-5.3` / `zhipu/glm-5.3` → `glm-5.3`）。归一后的标准名用于：
   选路、Key 模型校验、计价、记录、响应。
2. **选路过滤**：listing 必须满足
   - `mp_channel_models.verified = true AND status = active`；
   - `mp_channels.status = active`；
   - 套餐渠道未处于额度暂停（见 P4）；
   - 买家 key 勾选「只用官方渠道」时 `mp_channels.kind = official`。
3. **上游名映射**：挂单创建/验真时把 `(channel_slug, model)` 的上游名写入 channel 的
   `model_mapping`，转发层标准模型 → 上游名。
4. **响应改写**：市场請求的响应（JSON 与 SSE）把 `"model"` 字段值改回标准名。
   实现为 gin ResponseWriter 包装器（仅市场请求启用），只替换请求上下文登记的
   上游名集合，避免误伤 content 里的同名字符串。换家时更新集合。
5. **计费**：`official price(标准模型, 原厂) × coefficient/1000 × multiplier/1000`；
   `OfficialCost` 仍为官方原价（multiplier=1000、不含系数），用于「省了多少」。
   `mp_requests` 与账单只出现标准名。

## 4. 认证与抽检（P3/P4）

- 渠道路径：平台 key → `/models` + 最小调用；套餐渠道额外人工核对额度窗口与条款链接。
- 模型路径：管理端从 `/models` 选上游名，跑指纹测试：
  - 同一段 `verify_prompt` 分别请求**官方渠道（平台 key）**与**候选渠道（卖家 key）**，
    比较 `usage.prompt_tokens` 比值；误差 ≤ 2% 视为一致；
  - 差异过大 → 单独标准名（计划建议）。
- 卖家挂单：选渠道 → 粘贴 key → 探针返回「该 key 可卖的已认证模型」→ 勾选 + 倍率 →
  指纹抽检 → canary（10%）→ 24h 成功率 ≥98% 转 live。
- 巡检：每次抽检写 `mp_probes`；连续 2 次不符 → 暂停，连续 3 次 → 封禁。
- 套餐额度耗尽：上游报错命中 `exhaustion_signature` → 挂单暂停到 `quota_window` 重置，
  `fail_reason=plan_quota_exhausted`，不计信用分、不算故障。

## 5. 前端（P5）

- 广场按标准名聚合：有挂单显示最低到手价；无挂单也展示官方价（广场不再 0 个模型）。
- 详情页按渠道分组列挂单，徽章：官方 / 套餐 / 已验真；展示差异（上下文、图片支持）。
- 卖家端：渠道选择器 + 可卖模型勾选（不再手填模型名/URL）。
- 管理后台：三张表 CRUD（`/admin/pool/models|channels|channel-models`）。
- 文档页模型列表由 `/marketplace/vendors` 与广场数据自动生成。

## 6. 迁移（幂等）

1. 建三表 + 种子（6 官方渠道 + 火山/百炼 Coding Plan；模型与可卖资格种子）。
2. 旧 `mp_channel_model_map` → `mp_channel_models`（verified=true, 系数 1000）。
3. 价目去重（保留原厂；重复四元组删除）。
4. 旧挂单：渠道/模型在池中无行 → `paused` + 写 `mp_probes(type=migration)` 提示重新验真。
5. 旧上游 Channel 行在验真/巡检时按新映射重建 `model_mapping`。

## 7. 分期与验收

按用户计划的 P1–P6 执行。验收以计划 §验收 为准：

- `glm5.3` / `GLM-5.3` / `zhipu/glm-5.3` 均路由到 `glm-5.3`，账单只有标准名。
- 未认证的渠道/模型后端拒绝。
- 套餐 key 填官方渠道被拒。
- 套餐额度耗尽自动暂停、窗口重置自动恢复。
- 指纹连续不符自动下架。
- E2E 覆盖一个官方渠道 + 一个套餐渠道（依赖平台测试 key）。

## 8. 验收整改后的决策（2026-10-25）

| 主题 | 最终决策 |
| --- | --- |
| 指纹判定 | 只有官方与候选都成功取到 `prompt_tokens` 才计入；相对误差 ≤5% 或绝对差 ≤3 token；官方 prompt <20 token 不可判定。连续不符只暂停当前挂单，不自动封禁渠道×模型，封禁必须人工确认 |
| 套餐额度 | 额度属于单个账号，耗尽只暂停触发它的挂单到窗口重置；仅 429/402/403 且命中签名才认定 |
| 价格系数 | 1..1000，渠道不得高于官方价；买家价格上限按 `倍率 × 系数` 的实际有效倍率比较 |
| 透传 | 只有所有所选模型的上游名 = 标准名时才开启；有改名时走映射转换，避免上游收到平台模型名 |
| 响应改名 | 支持上游名 + 日期/版本后缀；改写后删除 Content-Length，由服务器重算 |
| 结算 | 使用预冻结时捕获的原厂与系数（换家时随新挂单更新），保证预冻结与实扣同一公式 |
| 管理后台改映射 | 立即重写所有相关挂单 channel 的 `model_mapping` |
| 卖家恢复 | 只能恢复自己手动暂停的挂单；平台暂停（migration/巡检/指纹/额度）必须人工处理 |
| 渠道 URL | 只接受 https；仅回环地址允许 http；禁止 userinfo；保存后刷新出网白名单 |
| 额度窗口 | 兼容原有零点对齐周期，新增 `mode=rolling`（滚动）和 `mode=weekly`（指定星期几 + 时分） |
| 挂单验真 | 验证 key 能调用所选每个模型（上游名，允许日期后缀），并用平台 key 做指纹抽检；不符只回 draft |
| 别名 | 只解析 active 模型；先查 name 再查 aliases；找不到时返回最近模型名建议 |
| 草稿迁移 | 迁移只暂停 live/canary 并写 `pause_reason=migration`；draft/verifying 不会被变成可恢复的 paused |
