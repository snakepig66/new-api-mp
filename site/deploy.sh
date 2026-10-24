#!/usr/bin/env bash
#
# 词元市场前端部署脚本（F5）。
#
# 只做编排：本地构建 site/dist → 通过 SSH 隧道传到 llmjar → 服务器上把
# MP_FRONTEND=site 写进 /etc/new-api.env → 重新编译 Go 二进制 → 重启服务。
# 不执行任何 git 操作，也不在脚本里保存任何口令、密钥或令牌。
#
# 用法：
#   cd site && ./deploy.sh
#
# 可用环境变量覆盖默认值：
#   DEPLOY_SSH_HOST   默认 127.0.0.1（instabox 本地转发端口 2222）
#   DEPLOY_SSH_PORT   默认 2222
#   DEPLOY_SSH_USER   默认 user
#   DEPLOY_SSH_KEY    默认 ~/.ssh/id_rsa
#   DEPLOY_TARGET_DIR 默认 /data/workspace/new-api
#   DEPLOY_ENV_FILE   默认 /etc/new-api.env
#   DEPLOY_SERVICE    默认 new-api
#   DEPLOY_GO_BIN     默认 go
#
set -euo pipefail

SITE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

SSH_HOST="${DEPLOY_SSH_HOST:-127.0.0.1}"
SSH_PORT="${DEPLOY_SSH_PORT:-2222}"
SSH_USER="${DEPLOY_SSH_USER:-user}"
SSH_KEY="${DEPLOY_SSH_KEY:-$HOME/.ssh/id_rsa}"
TARGET_DIR="${DEPLOY_TARGET_DIR:-/data/workspace/new-api}"
ENV_FILE="${DEPLOY_ENV_FILE:-/etc/new-api.env}"
SERVICE="${DEPLOY_SERVICE:-new-api}"
GO_BIN="${DEPLOY_GO_BIN:-go}"

SSH=(ssh -p "$SSH_PORT" -i "$SSH_KEY" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new "$SSH_USER@$SSH_HOST")

step() { printf '\n==> %s\n' "$*"; }
fail() { printf 'deploy: %s\n' "$*" >&2; exit 1; }

[ -f "$SSH_KEY" ] || fail "SSH 私钥不存在：$SSH_KEY"
[ -f "$SITE_DIR/package.json" ] || fail "找不到 $SITE_DIR/package.json"

step "1/6 本地构建前端（bun run build）"
(cd "$SITE_DIR" && bun run build)
[ -f "$SITE_DIR/dist/index.html" ] || fail "构建产物缺少 dist/index.html"

step "2/6 检查 SSH 连通性（$SSH_USER@$SSH_HOST:$SSH_PORT）"
"${SSH[@]}" 'printf "remote: %s\n" "$(hostname)"' || fail "SSH 连接失败"

step "3/6 上传 site/dist 到 $TARGET_DIR/site/dist（tar over ssh）"
"${SSH[@]}" "sudo mkdir -p '$TARGET_DIR/site/dist' && sudo chown -R \"\$(id -u):\$(id -g)\" '$TARGET_DIR/site/dist'"
tar -C "$SITE_DIR/dist" -czf - . | "${SSH[@]}" "tar -xzf - -C '$TARGET_DIR/site/dist'"

step "4/6 确保 $ENV_FILE 含 MP_FRONTEND=site"
"${SSH[@]}" "
  set -e
  if ! sudo test -f '$ENV_FILE'; then sudo touch '$ENV_FILE'; fi
  if sudo grep -q '^[[:space:]]*MP_FRONTEND=' '$ENV_FILE'; then
    sudo sed -i 's|^[[:space:]]*MP_FRONTEND=.*|MP_FRONTEND=site|' '$ENV_FILE'
  else
    printf '\nMP_FRONTEND=site\n' | sudo tee -a '$ENV_FILE' >/dev/null
  fi
  echo 'MP_FRONTEND 已设为 site'
"

step "5/6 服务器上重新编译（CGO_ENABLED=0 $GO_BIN build）"
"${SSH[@]}" "cd '$TARGET_DIR' && CGO_ENABLED=0 '$GO_BIN' build -o new-api ."

step "6/6 重启 systemd 服务（sudo systemctl restart $SERVICE）"
"${SSH[@]}" "sudo systemctl restart '$SERVICE' && sleep 1 && sudo systemctl --no-pager --lines=5 status '$SERVICE' || true"

printf '\n完成：前端已部署，服务 %s 已重启。\n' "$SERVICE"
