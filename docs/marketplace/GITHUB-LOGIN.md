# GitHub 登录接入（运维配置）

登录 / 注册页的「使用 GitHub 登录」按钮已实现完整链路：

1. 前端 `POST /api/oauth/state` 取一次性 `flow_token`（CSRF state），跳转
   `https://github.com/login/oauth/authorize`；
2. GitHub 回调到本站 SPA 路由 `/oauth/github`（携带 `code` + `state`）；
3. 该页调用 `GET /api/oauth/github?code=&state=`，后端用 code 换 token、拉取
   用户信息，按 GitHub 数字 ID 查找或创建账号，返回登录会话；
4. 前端保存会话并跳回登录前页面（默认 `/console`）。

代码无需改动即可用，只差一次性配置。

## 1. 创建 GitHub OAuth App

打开 <https://github.com/settings/applications/new>（组织账号则在组织设置里建）：

| 字段 | 值 |
| --- | --- |
| Application name | 词元市场（任意） |
| Homepage URL | `https://<本站域名>/` |
| Authorization callback URL | `https://<本站域名>/oauth/github` |

回调地址**必须**指向 SPA 路由 `/oauth/github`，不是 `/api/oauth/github`：
页面负责把 `code` 交给后端换取会话。创建后点「Generate a new client
secret」，得到 `Client ID` 与 `Client secret`。

> 本地 / 多环境：每个域名各建一个 OAuth App，回调地址与访问域名保持一致。

## 2. 配置到环境变量

环境变量优先级高于后台选项表（每次从数据库同步配置后会重新应用），
配置后重启即生效：

```bash
# /etc/new-api.env
GITHUB_CLIENT_ID=Iv1.xxxxxxxxxxxx
GITHUB_CLIENT_SECRET=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
# 可选：不写时，两个凭据都配齐就自动开启；写 false 可强制关闭
# GITHUB_OAUTH_ENABLED=true
```

```bash
sudo systemctl restart new-api
```

注意：

- `GITHUB_CLIENT_SECRET` 是密钥，**不要**提交到仓库、日志或工单；
  泄露后到 GitHub 页面重置（Reset client secret）再更新环境变量。
- `Client ID` 会通过 `/api/status` 公开返回（前端拼授权链接需要），这是
  OAuth 规范内的公开值，不是密钥。
- 后台选项表（旧控制台「系统设置 → GitHub OAuth」）仍然可用；环境变量
  只覆盖它，不会写回数据库。不配任何环境变量时行为完全不变。

## 3. 验证

```bash
# 期望 data.github_oauth = true，data.github_client_id 为你的 Client ID
curl -s https://<本站域名>/api/status | jq '.data | {github_oauth, github_client_id}'
```

浏览器打开 `/login` → 「使用 GitHub 登录」应可点击 → 授权后回到本站并已登录。
若按钮仍提示「站点还没有开启 GitHub 登录」，说明 `/api/status` 还是
`github_oauth: false`：检查两个环境变量是否都在同一个进程环境里、是否重启过。

## 4. 行为说明

- 账号识别用 GitHub 数字 ID（永久不变），用户名改名不会产生新账号，也
  不会因改名抢注别人的账号。
- 首次登录自动创建账号（受「允许新用户注册」开关约束）；已登录用户走
  绑定流程而不是切换账号。
- 启用了两步验证策略的账号，OAuth 登录会返回二次验证挑战，由前端引导完成，
  与密码登录一致。
- 请求只申请 `user:email` 范围。
