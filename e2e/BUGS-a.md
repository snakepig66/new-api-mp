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

---

## Bug 3: ValidateEgressIP 未支持 MP_ALLOW_LOOPBACK_BASE_URL 导致本地环回上游验真拨号被拒

- **严重程度**: 中
- **复现步骤**:
  1. 设置 `MP_ALLOW_LOOPBACK_BASE_URL=1` 以支持本地开发测试环回上游。
  2. 卖家提交自定义渠道（如 `http://127.0.0.1:3201/v1`）进行验真。
  3. `custom_channel.go` 通过了基础 URL 校验，但在发起验真探测时，`ValidateEgressIP` 无条件拒绝了环回 IP `127.0.0.1`，报错 `non-public ip`。
- **预期**: 当设置了 `MP_ALLOW_LOOPBACK_BASE_URL=1` 时，`ValidateEgressIP` 应允许环回 IP 访问以支持本地测试。
- **实际**: `ValidateEgressIP` 无条件阻断环回地址，导致开发环境下无法验真本地假上游。
- **修复**: 在 `marketplace/egress.go` 的 `ValidateEgressIP` 中，当检测到环回地址且环境变量 `MP_ALLOW_LOOPBACK_BASE_URL=1` 时放行。补全单测覆盖。

---

## Bug 4: CheckEgressHost 在开启 MP_ALLOW_LOOPBACK_BASE_URL 时仍拦截环回 IP 导致中继转发失败

- **严重程度**: 中
- **复现步骤**:
  1. 设置 `MP_ALLOW_LOOPBACK_BASE_URL=1` 以支持本地开发测试环回上游。
  2. 卖家提交自定义渠道（如 `http://127.0.0.1:3201/v1`）审核通过并上线（live）。
  3. 客户端通过密钥发起调用，中继经由 `EgressRelayHTTPClient` 拨号，底层 `dialCheckedPolicy` 执行 `CheckEgressHost("127.0.0.1")`。
- **预期**: 当开启 `MP_ALLOW_LOOPBACK_BASE_URL=1` 时，`CheckEgressHost` 应允许环回 IP 访问以支持本地测试及中继。
- **实际**: `CheckEgressHost` 无条件拒绝所有 IP 字面量（报错 `ip literal "127.0.0.1" is not a whitelisted domain`），导致即便验真通过，中继拨号时依然被拦截并返回 500/503。
- **修复**: 在 `marketplace/egress.go` 的 `CheckEgressHost` 中，当解析为 IP 且满足 `parsed.IsLoopback() && os.Getenv(MPAllowLoopbackBaseURLEnv) == "1"` 时予以放行。在 `marketplace/egress_test.go` 中补充测试覆盖。


