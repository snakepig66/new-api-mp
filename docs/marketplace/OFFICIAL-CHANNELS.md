# 官方渠道白名单（第一批）

定义：「官方渠道」是指模型厂商自己的开放平台，或大厂云平台的按量付费推理接口。卖家只需选平台并粘贴 key，base_url 由平台固定，不能手填。
「第三方渠道」是自定义 URL，按 CUSTOM-URL 方案单独成一档。

## 1. 白名单（按 host + 路径前缀精确匹配，不按域名放行）
| 平台 | 渠道 ID | OpenAI 兼容 | Anthropic 兼容 | 备注 |
|---|---|---|---|---|
| DeepSeek 官网 | deepseek | https://api.deepseek.com | https://api.deepseek.com/anthropic | 现有 |
| 火山方舟 | volcengine-ark | https://ark.cn-beijing.volces.com/api/v3 | https://ark.cn-beijing.volces.com/api/compatible | 托管豆包、DeepSeek、Kimi、GLM 等；Anthropic 接口只支持部分模型 |
| 阿里云百炼（北京） | aliyun-bailian | https://dashscope.aliyuncs.com/compatible-mode/v1 | https://dashscope.aliyuncs.com/apps/anthropic | 也接受业务空间专属域名 `{ws}.cn-beijing.maas.aliyuncs.com`（同路径，ws 只允许 `[a-z0-9-]`） |
| 阿里云百炼（新加坡） | aliyun-bailian-intl | https://dashscope-intl.aliyuncs.com/compatible-mode/v1 | https://dashscope-intl.aliyuncs.com/apps/anthropic | 可选，第二批再开 |
| Kimi（月之暗面） | moonshot | https://api.moonshot.cn/v1 | https://api.moonshot.cn/anthropic | 上线前用探针核实 |
| 智谱 BigModel | zhipu | https://open.bigmodel.cn/api/paas/v4 | https://open.bigmodel.cn/api/anthropic | 上线前用探针核实 |
| MiniMax | minimax | https://api.minimaxi.com/v1 | https://api.minimaxi.com/anthropic | 上线前用探针核实 |

第二批候选：腾讯混元（api.hunyuan.cloud.tencent.com/v1）、百度千帆（qianfan.baidubce.com/v2）。
硅基流动这类聚合平台不算官方，归入第三方。

## 2. 套餐接口与套餐 key 的处理

官方渠道（`kind=official`）只接受按量付费的官方 key；套餐额度不是按量商品，默认不在官方渠道转售。

| 场景 | 处理 |
|---|---|
| 官方渠道收到套餐路径 / key | 直接拒绝：火山 `/api/coding`、`/api/plan`；百炼 `coding.dashscope.aliyuncs.com`、`token-plan.*.maas.aliyuncs.com`、`sk-sp-` / `sk-ws-` 前缀；智谱 `/api/coding/paas/v4` |
| 卖家想把套餐 key 上架 | 只能选择独立的 `kind=plan` 渠道（火山 Coding Plan、百炼 Coding Plan），由管理员维护 `key_pattern`、`quota_window`、`exhaustion_signature` |
| 套餐渠道额度耗尽 | 只暂停当前这一个挂单到下一个额度窗口，不暂停整个渠道、不算故障；窗口到期自动恢复 |
| key 填错渠道 | 套餐 key 填官方渠道或官方 key 填套餐渠道都会被 `key_pattern` 拒绝，换 key 时同样校验 |

套餐渠道上架前必须核对厂商条款并由管理员在管理后台录入 `terms_url`、`terms_checked_at`；平台不代替卖家判断转售是否被允许。套餐额度是**每个账号各自**的，所以任何一次耗尽都只影响对应挂单，不能牵连同渠道的其他卖家。

## 3. 云平台托管模型的定价与验真
- 火山、百炼托管的第三方模型（例如火山上的 DeepSeek-V3.2），按**模型原厂官方价**计价、按卖家倍率结算。不使用云平台自己的价格，避免同一个模型在广场上出现两个基准价。
- 云平台的模型名和原厂不一样（火山 `deepseek-v3-2-xxxxxx`、百炼 `deepseek-v3.2`），需要一张 `channel_model_map`：渠道 + 上游模型名 → 平台标准模型 ID。只有映射表里有的模型才能上架。
- 火山可能用接入点 ID（`ep-xxxx`）调用：挂单时让卖家选模型，平台探测出对应的接入点后保存，买家看不到这个 ID。
- 探针按渠道分别实现：验 key、余额（能查的话）、可用模型列表、一次最小调用，返回的 model 字段必须和映射一致。

## 4. 代码改动
1. `vendors.go`：每个渠道定义 `{id, name, tier: official, protocols: {openai: url, anthropic: url}, allowHostPattern, denyPaths, denyKeyPrefixes, adaptor}`。tipig 从这里删除，改为第三方挂单。
2. 出网白名单直接从 vendors 生成；百炼专属域名用严格的正则，不使用通配。
3. 新增 `channel_model_map` 表和种子数据（后台可编辑）。
4. 前端：挂单向导第 1 步分「官方渠道 / 第三方渠道」两张卡；官方渠道是平台 logo 网格（DeepSeek、火山方舟、阿里云百炼、Kimi、智谱、MiniMax）；模型广场的标签统一叫「官方渠道」。
5. 文案统一：「官方直连」全部改为「官方渠道」。

## 5. 依据（2026-10-25 查阅）
- 火山方舟 Base URL 及鉴权：https://ark.volcengine.com/region:cn-beijing/docs/base-url-and-authentication
- 火山方舟 接入三方工具（API 与 Plan 地址对照）：https://ark.volcengine.com/region:cn-beijing/docs/integrate-third-party-tools
- 百炼 Base URL 总览：https://help.aliyun.com/zh/model-studio/base-url
- 百炼 API Key 前缀与套餐对应：https://docs.modelstudio.console.alibabacloud.com/zh/model-studio/get-api-key
