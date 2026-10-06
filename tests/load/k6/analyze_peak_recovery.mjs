import { readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import assert from "node:assert/strict";

const root = resolve(process.argv[2]);
const read = async p => JSON.parse((await readFile(p, "utf8")).replace(/^\uFEFF/, ""));
const acceptedKey = 'http_requests_total{method="POST",path="/api/v1/rush-sales/:id/execute",status="200"}';
const confirmedKey = "ticket_order_accepted_to_pending_payment_duration_seconds_count";
function quantile(first, last, q) {
  const prefix = "ticket_order_accepted_to_pending_payment_duration_seconds_bucket{";
  const buckets = Object.keys(last.prometheus).filter(k => k.startsWith(prefix)).map(k => ({
    upper: Number(k.match(/le="([^"]+)"/)[1].replace("+Inf", "Infinity")),
    count: last.prometheus[k] - (first.prometheus[k] || 0),
  })).sort((a,b) => a.upper-b.upper);
  const rank = buckets.at(-1).count*q;
  let lower=0, previous=0;
  for (const b of buckets) {
    if (b.count >= rank) return Number.isFinite(b.upper) ? lower+(b.upper-lower)*(rank-previous)/(b.count-previous) : lower;
    lower=b.upper;previous=b.count;
  }
}
const rows=[];
for (const mode of ["limited", "buffered"]) {
  const path=join(root,mode,"vus-400");
  const k6=await read(join(path,"k6-summary.json"));
  const samples=await read(join(path,"lifecycle-samples.json"));
  const verification=await read(join(root,mode,"verification.json"));
  const first=samples[0], last=samples.at(-1);
  const delta=(key,s=last) => (s.prometheus[key]||0)-(first.prometheus[key]||0);
  const pending=s => Math.max(0,delta(acceptedKey,s)-delta(confirmedKey,s));
  const active=samples.filter(s => s.phase==="http" && delta(acceptedKey,s)>0);
  const origin=Date.parse(active[0].timestamp);
  const recovery=active.filter(s => Date.parse(s.timestamp)>=origin+130000);
  const steady=active.filter(s => Date.parse(s.timestamp)<origin+115000);
  const normalPending=Math.max(...steady.map(pending));
  const normalMQ=Math.max(...steady.map(s=>s.rabbitmq.work_queue_total));
  const recovered=recovery.find((s,i)=>i+2<recovery.length && recovery.slice(i,i+3).every(
    v=>pending(v)<=normalPending && v.rabbitmq.work_queue_total<=normalMQ));
  const peak=Math.max(...active.map(pending));
  const clear=recovery.find(s => pending(s)===0);
  assert.equal(delta(confirmedKey), verification.checks[0].confirmed);
  assert.equal(k6.phases.reduce((n,p)=>n+p.accepted,0),verification.checks[0].orders);
  rows.push({mode,phases:k6.phases,dropped_iterations:k6.dropped_iterations,
    server_errors:k6.rush_execute_server_error,transport_errors:k6.rush_execute_transport_error,
    pending_peak:peak,pending_at_http_end:pending(active.at(-1)),
    pending_at_recovery_first_sample:recovery.length?pending(recovery[0]):null,
    recovery_first_zero_seconds:clear?(Date.parse(clear.timestamp)-origin)/1000-130:null,
    recovery_to_normal_seconds:recovered?(Date.parse(recovered.timestamp)-origin)/1000-130:null,
    normal_pending_threshold:normalPending,normal_mq_threshold:normalMQ,
    mq_peak:Math.max(...active.map(s=>s.rabbitmq.work_queue_total)),
    confirmed_orders:delta(confirmedKey),confirmation_average_seconds:delta("ticket_order_accepted_to_pending_payment_duration_seconds_sum")/delta(confirmedKey),
    confirmation_p95_seconds:quantile(first,last,.95),confirmation_p99_seconds:quantile(first,last,.99),
    pool_in_use_peak:Math.max(...active.map(s=>s.prometheus.go_sql_db_in_use||0)),
    pool_wait_count:delta("go_sql_db_wait_count"),verification_passed:verification.passed,
  });
}
await writeFile(join(root,"analysis.json"),JSON.stringify(rows,null,2));
const f=n=>n==null?"—":Number(n).toFixed(2);
const lines=["# 下单入口限流与 MQ 削峰压测","",
  "单实例：HTTP 与后台共享 100 个数据库连接，12 Consumer，4 Publisher，32 个库存分桶；资源与阶梯压测相同，MySQL redo/binlog 双 1。",
  "",
  "输入：150/s 持续 120 秒 → 500/s 突发 10 秒 → 50/s 持续 60 秒 → 停止输入并核对清空。limited 为入口令牌桶 150/s、burst 50；buffered 放宽入口配额，单独验证队列缓冲。用户/IP/全局限流保持宽松。",
  "",
  "| 模式 | 阶段 | 输入/s | 实际请求 | 受理 | 限流拒绝 | 受理 P95 ms | 受理 P99 ms |",
  "|---|---|---:|---:|---:|---:|---:|---:|",
  ...rows.flatMap(r=>r.phases.map(p=>`| ${r.mode} | ${p.name} | ${p.rate} | ${p.requests} | ${p.accepted} | ${p.rate_limited} | ${f(p.accepted_latency_ms?.["p(95)"])} | ${f(p.accepted_latency_ms?.["p(99)"])} |`)),
  "",
  "| 模式 | 未发出请求 | 待确认峰值 | MQ 峰值 | 输入结束待确认 | 降流后恢复正常积压 s | 确认 P99 s | DB 连接峰值 | DB 等待次数 |",
  "|---|---:|---:|---:|---:|---:|---:|---:|---:|",
  ...rows.map(r=>`| ${r.mode} | ${r.dropped_iterations} | ${r.pending_peak} | ${r.mq_peak} | ${r.pending_at_http_end} | ${f(r.recovery_to_normal_seconds)} | ${f(r.confirmation_p99_seconds)} | ${r.pool_in_use_peak} | ${r.pool_wait_count} |`),
  "",
  "待确认量按受理数减实际订单状态确认数计算，不把 Outbox 和 MQ 重复相加。峰值为定期采样观察值。降流时间按首次有受理的采样点对齐，为近似值；首次零积压不代表之后所有采样点均为零。确认分位数为全程 Prometheus 直方图估计。HTTP 延迟仅统计成功受理，避免快速 429 掩盖慢请求。",
  "恢复正常积压：连续三个采样点的待确认量与 MQ 工作队列量均回到本轮突发前观察的正常峰值以内，时间取三个点的第一个；持续有 50/s 新请求时允许少量在途订单。停止输入后再核对完全清空。",
  "",
  ...rows.map(r=>`${r.mode}：最终确认 ${r.confirmed_orders} 单；订单、Inbox、Redis/MySQL 库存、看板核对${r.verification_passed?"通过":"失败"}；服务端异常 ${r.server_errors}，传输异常 ${r.transport_errors}。`),
];
await writeFile(join(root,"report.md"),lines.join("\n"));
console.log(JSON.stringify(rows));
