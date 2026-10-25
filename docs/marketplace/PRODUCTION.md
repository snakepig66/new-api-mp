# LLM Jar 生产部署（阿里云香港）

## 服务器

- 公网 IP：`47.76.231.181`
- 系统：Ubuntu 22.04.5 LTS x86_64
- 应用目录：`/opt/llmjar`
- 源码目录：`/opt/llmjar/src`
- 二进制：`/opt/llmjar/new-api-linux`
- SQLite：`/opt/llmjar/one-api.db`
- 环境文件：`/etc/llmjar.env`（含 `MP_KEY_SECRET`，权限 `600 root:root`）
- systemd：`llmjar.service`，运行用户 `llmjar`
- nginx：`/etc/nginx/sites-available/llmjar`，80 → `127.0.0.1:3000`
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

## 重要约束

- `MP_KEY_SECRET` 必须另行备份；丢失后所有卖家上游 Key 无法解密。
- 当前站点是 HTTP，`ServerAddress=http://47.76.231.181`。有域名后：
  1. 在 nginx 上配置 443 和证书（或换 Caddy 自动证书）。
  2. 把 `ServerAddress` 改为 `https://域名`。
  3. 在 `/etc/llmjar.env` 增加 `SESSION_COOKIE_SECURE=true` 并重启。
- 支付配置（`PayAddress` / `EpayId` / `EpayKey`）和 SMTP 目前为空，充值、忘记密码入口保持关闭；配置后 epay 回调地址为 `<ServerAddress>/api/user/epay/notify`。
- 旧 instabox 机器保留作为回滚，确认新站稳定后再停用。
- 生产机安全组只需放行 22、80、443；不要对公网开放 3000，nginx 已负责反代。
