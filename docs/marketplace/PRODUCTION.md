# LLM Jar 生产部署（阿里云香港）

## 服务器

- 生产地址：`https://llmjar.com`（`www.llmjar.com` 同样可用）
- 公网 IP：`47.76.231.181`
- DNS：Cloudflare，`A @` 与 `A www` 均指向 `47.76.231.181`（DNS only）
- 系统：Ubuntu 22.04.5 LTS x86_64
- 应用目录：`/opt/llmjar`
- 源码目录：`/opt/llmjar/src`
- 二进制：`/opt/llmjar/new-api-linux`
- SQLite：`/opt/llmjar/one-api.db`
- 环境文件：`/etc/llmjar.env`（含 `MP_KEY_SECRET`，权限 `600 root:root`）
- systemd：`llmjar.service`，运行用户 `llmjar`
- nginx：`/etc/nginx/sites-available/llmjar`；80 全量 301 到 `https://llmjar.com`，443 → `127.0.0.1:3000`
- 证书：Let's Encrypt，`/etc/letsencrypt/live/llmjar.com/`，certbot 自动续期
- 日志：`journalctl -u llmjar -f`

## 常用命令

```bash
systemctl status llmjar
systemctl restart llmjar
journalctl -u llmjar -n 100 --no-pager
nginx -t && systemctl reload nginx
curl -s http://127.0.0.1:3000/api/status
```

## 更新代码

本地构建前端后，把源码和 `site/dist` 一起上传：

```bash
cd site && bun run build

cd ..
tar --exclude=.git --exclude=node_modules --exclude=logs --exclude='*.db' \
  -czf /tmp/llmjar-src.tgz .

scp -i ~/.ssh/llmjar_prod_ed25519 /tmp/llmjar-src.tgz root@47.76.231.181:/opt/llmjar/

ssh -i ~/.ssh/llmjar_prod_ed25519 root@47.76.231.181 '
  set -e
  cd /opt/llmjar/src
  tar -xzf ../llmjar-src.tgz
  CGO_ENABLED=0 GOPROXY=https://goproxy.cn,direct /usr/local/go/bin/go build -o /opt/llmjar/new-api-linux .
  chown llmjar:llmjar /opt/llmjar/new-api-linux
  systemctl restart llmjar
  rm -f /opt/llmjar/llmjar-src.tgz
'
```

Go 版本为 1.26.10，安装在 `/usr/local/go`。

## 数据库备份

```bash
ssh -i ~/.ssh/llmjar_prod_ed25519 root@47.76.231.181 '
  cd /opt/llmjar
  python3 - <<"PY"
import sqlite3
src = sqlite3.connect("one-api.db")
dst = sqlite3.connect("/opt/llmjar/one-api.db.bak")
with dst:
    src.backup(dst)
src.close(); dst.close()
PY
  chown llmjar:llmjar /opt/llmjar/one-api.db.bak
'
```

恢复时先停服务，替换 `one-api.db`，确认属主为 `llmjar:llmjar` 后启动。

## 旧中转站网关（codely / clinepass）

旧站 `api.tipig.cloud` 的两个自研网关已全量迁到本机 `/opt`，Node 22 运行：

| 服务 | 端口 | 目录 | 说明 |
|---|---|---|---|
| `codely-proxy` | 8791 | `/opt/codely-bridge` | codely 内部代理 |
| `codely-gate` | 8790 | `/opt/codely-bridge` | codely 鉴权门（依赖 8791） |
| `codely-cachehit` | 8792 | `/opt/codely-bridge` | 缓存命中网关（依赖 8790） |
| `clinepass` | 3123 | `/opt/clinepass` | Cline Pass 多账号代理 |
| `clinepass-bridge` | 3126 | `/opt/clinepass-bridge` | Responses 协议转换（依赖 3123） |

链路：

```text
new-api(ch9)  → 127.0.0.1:8792 → 8790 → 8791 → codely 上游
new-api(ch18) → 127.0.0.1:3126 → 3123 → Cline 上游
```

- 渠道 9 `codely` 的 `base_url` 已改为 `http://127.0.0.1:8792`
- 渠道 18 `clinepass` 的 `base_url` 已改为 `http://127.0.0.1:3126`
- 两个渠道的 `abilities` 与模型定价（`ModelPrice` / `ModelRatio` / `CompletionRatio` / `billing_setting.*`）已从旧库合并
- 实测通过新站 `/v1`：`glm-5.3` 与 `deepseek-v4.1-flash-1017` 均返回 200

验证命令：

```bash
ssh -i ~/.ssh/llmjar_prod_ed25519 root@47.76.231.181 \
  'for s in codely-proxy codely-gate codely-cachehit clinepass clinepass-bridge; do systemctl is-active $s; done'
```

注意：

- 网关凭据在 `/opt/codely-bridge/accounts`、`codely-creds.json`、`api-key.txt`、`key.cache`、`session.cache`，以及 `/opt/clinepass/config.json`；这些是运行必需，需与 `MP_KEY_SECRET` 一样单独备份。
- 旧站其余指向 `172.17.0.1:8797`（cache-shave）和 `8799`（responses-adapter）的渠道尚未迁网关，仍不可用：ch5、ch31、ch34、ch35、ch37、ch38、ch39。

## 重要约束

- `MP_KEY_SECRET` 必须另行备份；丢失后所有卖家上游 Key 无法解密。
- `/etc/llmjar.env` 必须固定 `SESSION_SECRET`（以及可选的 `CRYPTO_SECRET`）。不设置时进程每次启动会生成新的 UUID，所有登录态会在重启/部署后失效。当前已写入持久随机值，需与 `MP_KEY_SECRET` 一起备份。
- 当前站点是 HTTPS：`ServerAddress=https://llmjar.com`。`/etc/llmjar.env` 中：
  - `SESSION_COOKIE_SECURE=true`
  - `SESSION_COOKIE_TRUSTED_URL=https://llmjar.com,https://www.llmjar.com`
- 证书续期检查：
  ```bash
  ssh -i ~/.ssh/llmjar_prod_ed25519 root@47.76.231.181 \
    'certbot renew --dry-run && systemctl list-timers | grep certbot'
  ```
- Cloudflare 如果以后打开橙云代理，SSL/TLS 模式需要设为 **Full (strict)**；当前保持 DNS only。
- 支付配置（`PayAddress` / `EpayId` / `EpayKey`）和 SMTP 目前为空，充值、忘记密码入口保持关闭；配置后 epay 回调地址为 `<ServerAddress>/api/user/epay/notify`。
- 旧 instabox 机器保留作为回滚，确认新站稳定后再停用。
- 生产机安全组只需放行 22、80、443；不要对公网开放 3000，nginx 已负责反代。
