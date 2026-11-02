# E2E 发现与修复问题清单 (分支 mp-a)

## Bug 1: OpenAI 渠道在接收 Claude 格式请求且未开启直通时错误路由至 Anthropic 端点

- **严重程度**: 高
- **复现步骤**:
  1. 创建一个类型为 OpenAI (Type=1) 的原生或普通渠道，Base URL 为 OpenAI 兼容地址（如 `http://127.0.0.1:3201/v1`），不包含 `/anthropic`，未开启直通。
  2. 客户端使用 Claude `/v1/messages` 格式请求该渠道支持的模型。
  3. 请求在经过协议转换后，计算出的上游请求 URL 错误地指向了 `/v1/messages` 而非 `/v1/chat/completions`。
- **预期**: 未开启直通且上游不是 Anthropic 端点时，已转为 OpenAI 协议的请求应发送至上游 `/v1/chat/completions`。
- **实际**: 发送到了 `/v1/messages`，导致纯 OpenAI 兼容上游返回 404 错误。
- **修复**: 在 `relay/channel/openai/adaptor.go` 中，仅当设置了 `MPAnthropicMessagesURL`、开启了直通或 Base URL 包含 `/anthropic` 时才路由至 `EndpointAnthropicMessages`，否则路由至 `EndpointOpenAIChat`。补全单测覆盖。

---

## Bug 2: 响应格式转换时泄露上游内部模型名

- **严重程度**: 中
- **复现步骤**:
  1. 上游真实返回的 JSON 中 `model` 字段为内部名（如 `upstream-fake-gpt-4o`）。
  2. 客户端调用 Claude 格式并经由 OpenAI 适配器转换，或者调用 OpenAI 格式经由 Claude 适配器转换。
- **预期**: 返回给调用方的响应模型名以及记录应使用用户请求的 `OriginModelName`，隐藏上游真实模型名。
- **实际**: 转换前未将 `simpleResponse.Model` 或 `claudeResponse.Model` 重写为 `info.OriginModelName`，导致经 `ConvertResponse` 格式转换后的响应暴露了上游真实模型名。
- **修复**: 在 `relay/channel/openai/relay-openai.go` 与 `relay/channel/claude/relay-claude.go` 中，在执行格式转换前先对响应的 Model 字段赋 `info.OriginModelName`。补全单测覆盖。
