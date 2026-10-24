#!/bin/bash
# MARKETPLACE-DEV.md §5 阶段 7：真实 DeepSeek key 端到端对账
# 用法: e2e.sh <DEEPSEEK_API_KEY>
set -uo pipefail
KEY="${1:?usage: e2e.sh <deepseek_key>}"
BASE=http://127.0.0.1:3000
DB=/data/workspace/new-api/one-api.db
MODEL=deepseek-flash
PASS='MpE2e#2026x'

sql() { python3 - "$DB" "$1" <<'PY'
import sqlite3,sys
c=sqlite3.connect(sys.argv[1])
r=c.execute(sys.argv[2])
for row in r.fetchall(): print('|'.join('' if v is None else str(v) for v in row))
c.commit()
c.close()
PY
}
step() { echo; echo "===== $* ====="; }

# login <user> -> echoes access_token
login() {
  curl -s -X POST $BASE/api/user/login -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"$PASS\"}" | jq -r '.data.access_token // empty'
}

step "0. 管理员就绪（无 root 则初始化）"
ROOT_INIT=$(curl -s $BASE/api/setup | jq -r '.data.root_init' 2>/dev/null || echo true)
if [ "$ROOT_INIT" = "false" ]; then
  curl -s -X POST $BASE/api/setup -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"'$PASS'","confirmPassword":"'$PASS'"}' | jq -c '{success,message}' || true
fi
ADMIN_T=$(login admin)
echo "admin token: ${ADMIN_T:0:16}..."

step "1. 分享者/买家注册并登录"
curl -s -X POST $BASE/api/user/register -H 'Content-Type: application/json' -d '{"username":"e2eseller","password":"'$PASS'"}' >/dev/null || true
curl -s -X POST $BASE/api/user/register -H 'Content-Type: application/json' -d '{"username":"e2ebuyer","password":"'$PASS'"}' >/dev/null || true
SELLER_T=$(login e2eseller)
BUYER_T=$(login e2ebuyer)
[ -z "$SELLER_T" ] && { echo "seller login FAILED"; exit 1; }
[ -z "$BUYER_T" ] && { echo "buyer login FAILED"; exit 1; }
SELLER_ID=$(sql "select id from users where username='e2eseller'")
BUYER_ID=$(sql "select id from users where username='e2ebuyer'")
echo "seller=$SELLER_ID buyer=$BUYER_ID"

step "2. 开通分享者身份 + 买家充值 100 元"
sql "insert or ignore into mp_sellers(user_id,status,real_name_status,agreement_version,credit_score,created_at,updated_at) values($SELLER_ID,'active','ok','v1',100,datetime('now'),datetime('now'))" >/dev/null
sql "update users set quota=100000000 where id=$BUYER_ID" >/dev/null
echo done

step "3. 挂单（异步验真，等 25s）"
curl -s -X POST $BASE/api/marketplace/seller/listings -H "Authorization: Bearer $SELLER_T" -H 'Content-Type: application/json' \
  -d '{"vendor_id":"deepseek","api_key":"'$KEY'","models":["'$MODEL'"],"multiplier_milli":1000,"concurrency":5}' | tee /tmp/e2e_listing.json | jq -c '{success,message,id:.data.id}' || true
LID=$(jq -r '.data.id // empty' /tmp/e2e_listing.json)
[ -z "$LID" ] && LID=$(sql "select id from mp_listings where seller_id=$SELLER_ID order by id desc limit 1")
echo "listing id=$LID"
[ -z "$LID" ] && { echo "listing creation FAILED"; exit 1; }
sleep 25
sql "update mp_listings set status='live' where id=$LID" >/dev/null
sql "select id,seller_id,vendor,multiplier_milli,status from mp_listings where id=$LID"
sql "select ok,latency_ms,substr(detail,1,140) from mp_probes where listing_id=$LID order by id desc limit 1"

step "4. 买家创建 Key 并添加模型"
curl -sL -X POST $BASE/api/token -H "Authorization: Bearer $BUYER_T" -H 'Content-Type: application/json' \
  -d '{"name":"e2e","expired_time":-1,"unlimited_quota":true}' >/dev/null || true
BKEY=$(sql "select key from tokens where user_id=$BUYER_ID order by id desc limit 1")
TID=$(sql "select id from tokens where user_id=$BUYER_ID order by id desc limit 1")
[ -z "$BKEY" ] && { echo "token creation FAILED"; exit 1; }
echo "buyer token=${BKEY:0:14}... id=$TID"
curl -s -X POST $BASE/api/marketplace/key/models -H "Authorization: Bearer $BUYER_T" -H 'Content-Type: application/json' \
  -d "{\"token_id\":$TID,\"model\":\"$MODEL\",\"price_cap_milli\":0}" | jq -c '{success,message}' || true

recon() {
  local label="$1"
  local rid; rid=$(sql "select request_id from mp_requests order by id desc limit 1")
  echo "--- 对账[$label] request_id=$rid"
  [ -z "$rid" ] && { echo "(无记录)"; return; }
  sql "select 'req',status,model,tier,price_version,multiplier_milli,input_tokens,output_tokens,total_debit_micro,official_cost_micro,search_count,usage_estimated from mp_requests where request_id='$rid'"
  sql "select 'ledger',entry_type,account,amount_micro,status from mp_ledger where request_id='$rid' order by entry_type"
  sql "select 'sum_signed',coalesce(sum(amount_micro),0) from mp_ledger where request_id='$rid'"
  sql "select 'identity_ok',coalesce(sum(case when entry_type in ('buyer_debit','tool_debit') then -amount_micro else 0 end),0)=coalesce(sum(case when entry_type='seller_credit' then amount_micro else 0 end),0)+coalesce(sum(case when entry_type='platform_fee' then amount_micro else 0 end),0) from mp_ledger where request_id='$rid'"
}
BUYER_QUOTA() { sql "select quota from users where id=$BUYER_ID"; }

Q0=$(BUYER_QUOTA)
step "5a. 空闲单（上海时间 $(TZ=Asia/Shanghai date +%H:%M)，周六全天空闲）"
curl -s -X POST $BASE/v1/chat/completions -H "Authorization: Bearer $BKEY" -H 'Content-Type: application/json' \
  -d '{"model":"'$MODEL'","messages":[{"role":"user","content":"用一句话解释什么是复式记账"}],"max_tokens":80}' | jq -c '{model,usage,err:.error.message}' || true
recon "空闲单"; Q1=$(BUYER_QUOTA)

step "5b. 高峰单（价目 v2 模拟高峰官方单价；时钟高峰窗由单测覆盖）"
sql "insert into mp_price_books(model,vendor,tier,version,input_micro_per_m_tok,output_micro_per_m_tok,cache_read_micro_per_m_tok,cache_write_micro_per_m_tok,effective_at,created_at) values('$MODEL','deepseek','offpeak',2,2000000,8000000,40000,2000000,datetime('now'),datetime('now'))" >/dev/null
curl -s -X POST $BASE/v1/chat/completions -H "Authorization: Bearer $BKEY" -H 'Content-Type: application/json' \
  -d '{"model":"'$MODEL'","messages":[{"role":"user","content":"用一句话解释什么是缓存命中"}],"max_tokens":80}' | jq -c '{model,usage,err:.error.message}' || true
sql "delete from mp_price_books where version=2" >/dev/null
recon "高峰单"; Q2=$(BUYER_QUOTA)

step "5c. 流式"
curl -sN -X POST $BASE/v1/chat/completions -H "Authorization: Bearer $BKEY" -H 'Content-Type: application/json' \
  -d '{"model":"'$MODEL'","messages":[{"role":"user","content":"数到5，每个数字一行"}],"max_tokens":60,"stream":true}' | grep -o '"usage":{[^}]*}' | tail -1 || true
recon "流式"; Q3=$(BUYER_QUOTA)

step "5d. Anthropic messages"
curl -s -X POST $BASE/v1/messages -H "Authorization: Bearer $BKEY" -H 'Content-Type: application/json' -H 'anthropic-version: 2023-06-01' \
  -d '{"model":"'$MODEL'","max_tokens":80,"messages":[{"role":"user","content":"用一句话解释什么是 API 网关"}]}' | jq -c '{model,type,usage,stop_reason,err:.error.message}' || true
recon "messages"; Q4=$(BUYER_QUOTA)

step "5e. 带搜索工具（v1 应入站剥离、不计搜索费）"
curl -s -X POST $BASE/v1/chat/completions -H "Authorization: Bearer $BKEY" -H 'Content-Type: application/json' \
  -d '{"model":"'$MODEL'","messages":[{"role":"user","content":"你好"}],"max_tokens":30,"tools":[{"type":"web_search"}]}' | jq -c '{model,usage,err:.error.message}' || true
recon "搜索(剥离)"
sql "select 'hops',substr(retry_chain,1,200) from mp_requests where request_id=(select request_id from mp_requests order by id desc limit 1)"
Q5=$(BUYER_QUOTA)

step "6. 买家余额轨迹（微元）"
echo "初始=$Q0 → 空闲=$Q1 → 高峰=$Q2 → 流式=$Q3 → messages=$Q4 → 搜索=$Q5"
echo; echo "===== E2E COMPLETE ====="
