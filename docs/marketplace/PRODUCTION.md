# LLM Jar 生产部署

## 服务器

- 生产地址：`https://llmjar.com`（`www.llmjar.com` 同样可用）
- DNS：Cloudflare，DNS only 模式
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

# 上传到生产服务器（使用部署密钥）
scp /tmp/llmjar-src.tgz <SERVER>:/opt/llmjar/

# 在生产服务器上执行
ssh <SERVER> '
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
ssh <SERVER> '
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

## 内部网关

旧中转站网关已迁移到本机，通过 `127.0.0.1` 内部端口提供服务。具体拓扑和端口配置属于内部运维信息，不在公开文档中记录。

## 重要约束

- `MP_KEY_SECRET` 必须另行备份；丢失后所有卖家上游 Key 无法解密。
- `/etc/llmjar.env` 必须固定 `SESSION_SECRET`（以及可选的 `CRYPTO_SECRET`）。不设置时进程每次启动会生成新的 UUID，所有登录态会在重启/部署后失效。当前已写入持久随机值，需与 `MP_KEY_SECRET` 一起备份。
- 当前站点是 HTTPS：`ServerAddress=https://llmjar.com`。`/etc/llmjar.env` 中：
  - `SESSION_COOKIE_SECURE=true`
  - `SESSION_COOKIE_TRUSTED_URL=https://llmjar.com,https://www.llmjar.com`
- 证书续期检查：
  ```bash
  ssh <SERVER> \
    'certbot renew --dry-run && systemctl list-timers | grep certbot'
  ```
- Cloudflare 如果以后打开橙云代理，SSL/TLS 模式需要设为 **Full (strict)**；当前保持 DNS only。
- 支付配置（`PayAddress` / `EpayId` / `EpayKey`）和 SMTP 目前为空，充值、忘记密码入口保持关闭；配置后 epay 回调地址为 `<ServerAddress>/api/user/epay/notify`。
- 生产机安全组只需放行 22、80、443；不要对公网开放 3000，nginx 已负责反代。
