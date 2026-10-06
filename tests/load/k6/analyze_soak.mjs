import { readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import assert from "node:assert/strict";

const root=resolve(process.argv[2]), live=process.argv.includes("--live");
const read=async path=>JSON.parse((await readFile(path,"utf8")).replace(/^\uFEFF/,""));
const acceptedKey='http_requests_total{method="POST",path="/api/v1/rush-sales/:id/execute",status="200"}';
const confirmedKey="ticket_order_accepted_to_pending_payment_duration_seconds_count";
let samples;
if (live) samples=(await readFile(join(root,"live.ndjson"),"utf8")).trim().split("\n").map(JSON.parse).filter(s=>s.prometheus);
else samples=await read(join(root,"run","vus-400","lifecycle-samples.json"));
const initial=samples[0];
const delta=(key,a,b)=>(b.prometheus[key]||0)-(a.prometheus[key]||0);
const active=samples.filter(s=>delta(acceptedKey,initial,s)>0 && (live||s.phase==="http"));
if (active.length<3) { console.log("Waiting for HTTP samples");process.exit(0); }
const timed=live?samples:(await readFile(join(root,"live.ndjson"),"utf8")).trim().split("\n").map(JSON.parse).filter(s=>s.prometheus);
const timedInitial=timed[0];
const timedActive=timed.find(s=>delta(acceptedKey,timedInitial,s)>0);
const origin=Date.parse(timedActive.timestamp)-delta(acceptedKey,timedInitial,timedActive)/150*1000;
const elapsed=s=>(Date.parse(s.timestamp)-origin)/1000;
const pending=s=>Math.max(0,delta(acceptedKey,initial,s)-delta(confirmedKey,initial,s));
function quantile(a,b,prefix,q) {
  const buckets=Object.keys(b.prometheus).filter(k=>k.startsWith(`${prefix}_bucket{`)).map(k=>({
    upper:Number(k.match(/le="([^"]+)"/)[1].replace("+Inf","Infinity")),count:delta(k,a,b),
  })).sort((a,b)=>a.upper-b.upper);
  if (!buckets.length||!buckets.at(-1).count) return null;
  const rank=buckets.at(-1).count*q;
  let lower=0,previous=0;
  for (const bucket of buckets) {
    if (bucket.count>=rank) return Number.isFinite(bucket.upper)?lower+(bucket.upper-lower)*(rank-previous)/(bucket.count-previous):lower;
    lower=bucket.upper;previous=bucket.count;
  }
}
function windowData(window) {
  const a=window[0],b=window.at(-1),seconds=(Date.parse(b.timestamp)-Date.parse(a.timestamp))/1000;
  const meanStage=stage=>{
    const count=delta(`ticket_order_consumer_stage_duration_seconds_count{stage="${stage}"}`,a,b);
    return count?delta(`ticket_order_consumer_stage_duration_seconds_sum{stage="${stage}"}`,a,b)/count*1000:null;
  };
  return {
    seconds,accepted_per_second:delta(acceptedKey,a,b)/seconds,confirmed_per_second:delta(confirmedKey,a,b)/seconds,
    pending_peak:Math.max(...window.map(pending)),pending_end:pending(b),
    transaction_average_ms:delta('ticket_order_consumer_transaction_duration_seconds_sum{result="success"}',a,b) / delta('ticket_order_consumer_transaction_duration_seconds_count{result="success"}',a,b)*1000,
    commit_average_ms:meanStage("commit"),order_lock_average_ms:meanStage("order_lock"),
    items_average_ms:meanStage("order_items_read"),tier_update_average_ms:meanStage("tier_bucket_update"),rush_update_average_ms:meanStage("rush_bucket_update"),
    confirmation_p99_seconds:quantile(a,b,"ticket_order_accepted_to_pending_payment_duration_seconds",.99),
    pool_in_use_peak:Math.max(...window.map(s=>s.prometheus.go_sql_db_in_use||0)),
    pool_wait_count:delta("go_sql_db_wait_count",a,b),pool_wait_seconds:delta("go_sql_db_wait_duration_seconds",a,b),
    app_cpu_cores:delta("process_cpu_seconds_total",a,b)/seconds,
    app_memory_peak_mb:Math.max(...window.map(s=>s.prometheus.process_resident_memory_bytes||0))/1024/1024,
    outbox_peak:Math.max(...window.map(s=>s.prometheus.ticket_order_outbox_pending_rows||0)),
    mq_peak:Math.max(...window.map(s=>s.rabbitmq?.work_queue_total??s.prometheus['mq_queue_ready_messages{queue="fuchang.it.order.queue"}']??0)),
    mysql_lock_waits:delta("mysql_innodb_row_lock_waits_total",a,b),
  };
}
const minutes=[];
for (let minute=0;minute<25;minute++) {
  const window=active.filter(s=>elapsed(s)>=minute*60&&elapsed(s)<(minute+1)*60);
  if (window.length<3) continue;
  minutes.push({minute,...windowData(window)});
}
await writeFile(join(root,live?"live-analysis.json":"analysis.json"),JSON.stringify({origin:new Date(origin).toISOString(),minutes},null,2));
if (live) { console.log(JSON.stringify(minutes.slice(-2)));process.exit(0); }
const k6=await read(join(root,"run","vus-400","k6-summary.json"));
const verification=await read(join(root,"run","verification.json"));
const final=samples.at(-1);
assert.equal(delta(confirmedKey,initial,final),verification.checks[0].confirmed);
assert.equal(k6.phases.reduce((n,p)=>n+p.accepted,0),verification.checks[0].orders);
function counterAt(key,time) {
  const next=timed.findIndex(s=>Date.parse(s.timestamp)>=time);
  assert.ok(next>0,"Missing timed counter boundary");
  const a=timed[next-1],b=timed[next];
  const weight=(time-Date.parse(a.timestamp))/(Date.parse(b.timestamp)-Date.parse(a.timestamp));
  return (a.prometheus[key]||0)+weight*delta(key,a,b);
}
for (const row of minutes) {
  const minute=k6.minutes.find(m=>m.minute===row.minute);
  row.http_p99_ms=minute?.accepted_latency_ms?.["p(99)"];
  row.http_p95_ms=minute?.accepted_latency_ms?.["p(95)"];
  row.accepted_sampled_per_second=row.accepted_per_second;
  row.accepted_per_second=minute.accepted/60;
  row.confirmed_sampled_per_second=row.confirmed_per_second;
  const confirmedStart=row.minute===0?(initial.prometheus[confirmedKey]||0):counterAt(confirmedKey,origin+row.minute*60000);
  const confirmedEnd=row.minute===24?(final.prometheus[confirmedKey]||0):counterAt(confirmedKey,origin+(row.minute+1)*60000);
  row.confirmed_per_second=(confirmedEnd-confirmedStart)/60;
}
const before=active.filter(s=>elapsed(s)>=540&&elapsed(s)<595);
const normal=Math.max(...before.map(pending));
const after=active.filter(s=>elapsed(s)>=610);
const recovered=after.find((s,i)=>i+2<after.length&&after.slice(i,i+3).every(v=>pending(v)<=normal));
const lockRows=samples.filter(s=>s.phase==="http").flatMap(s=>s.mysql_lock_waits);
const countBy=key=>Object.fromEntries([...new Set(lockRows.map(r=>r[key]))].map(value=>[value,lockRows.filter(r=>r[key]===value).length]));
const result={minutes,k6,verification,origin:new Date(origin).toISOString(),
  pending_peak:Math.max(...active.map(pending)),pending_at_http_end:pending(active.at(-1)),
  burst_recovery_seconds:recovered?elapsed(recovered)-610:null,
  confirmation_p99_seconds:quantile(initial,final,"ticket_order_accepted_to_pending_payment_duration_seconds",.99),
  pool_wait_count:delta("go_sql_db_wait_count",initial,final),
  pool_wait_seconds:delta("go_sql_db_wait_duration_seconds",initial,final),
  pool_in_use_peak:Math.max(...active.map(s=>s.prometheus.go_sql_db_in_use)),
  transaction_average_ms:delta('ticket_order_consumer_transaction_duration_seconds_sum{result="success"}',initial,final) / delta('ticket_order_consumer_transaction_duration_seconds_count{result="success"}',initial,final)*1000,
  commit_average_ms:delta('ticket_order_consumer_stage_duration_seconds_sum{stage="commit"}',initial,final) / delta('ticket_order_consumer_stage_duration_seconds_count{stage="commit"}',initial,final)*1000,
  reconciliation_mismatches:Object.fromEntries(Object.keys(final.prometheus).filter(k=>k.startsWith("ticket_stock_reconciliation_mismatch_total{")).map(k=>[k,delta(k,initial,final)])),
  lock_wait_samples:lockRows.length,lock_wait_indexes:countBy("requesting_index"),lock_wait_modes:countBy("requesting_lock_mode"),
  lock_wait_sql:countBy("requesting_sql"),
};
const mean=rows=>rows.reduce((n,r)=>n+r.commit_average_ms,0)/rows.length;
result.first_five_commit_average_ms=mean(minutes.filter(r=>r.minute<5));
result.last_five_commit_average_ms=mean(minutes.filter(r=>r.minute>=20));
result.worst_http_minute=minutes.reduce((a,b)=>b.http_p99_ms>a.http_p99_ms?b:a);
await writeFile(join(root,"analysis.json"),JSON.stringify(result,null,2));
const f=n=>n==null?"—":Number(n).toFixed(2);
const lines=["# 25 分钟持续下单压测","",
  "配置：单后端 2 CPU/1GB，MySQL 2 CPU/2GB，Redis/MQ 各 1 CPU，12 Consumer、4 Publisher、32 库存分桶、共享 100 个数据库连接、redo/binlog 双 1。入口限流放宽，订单超时 60 分钟；5 万用户、50 万库存，单用户限购保持 20。",
  "",
  "负载：150/s 持续 10 分钟 → 500/s 突发 10 秒 → 150/s 持续至总时长 25 分钟 → 停止输入等待清空。",
  "",
  `实际请求 ${k6.http_reqs}；受理成功率 ${f(k6.rush_execute_success_rate*100)}%；未发出 ${k6.dropped_iterations}；服务端异常 ${k6.rush_execute_server_error}；传输异常 ${k6.rush_execute_transport_error}；限流拒绝 ${k6.rush_execute_rate_limited}。`,
  `待确认峰值 ${result.pending_peak}；输入结束待确认 ${result.pending_at_http_end}；突发结束后约 ${f(result.burst_recovery_seconds)} 秒回到突发前正常待确认范围；全程订单确认 P99 约 ${f(result.confirmation_p99_seconds)} 秒。`,
  `全程 HTTP P95 ${f(k6.p95_ms)}ms、P99 ${f(k6.p99_ms)}ms；最差分钟为第 ${result.worst_http_minute.minute} 分钟，HTTP P99 ${f(result.worst_http_minute.http_p99_ms)}ms。`,
  `Consumer 事务平均 ${f(result.transaction_average_ms)}ms，提交平均 ${f(result.commit_average_ms)}ms；前五分钟提交均值约 ${f(result.first_five_commit_average_ms)}ms，后五分钟约 ${f(result.last_five_commit_average_ms)}ms。数据库连接峰值 ${result.pool_in_use_peak}/100，等待 ${result.pool_wait_count} 次，累计等待 ${f(result.pool_wait_seconds)} 秒（所有调用的等待相加）。`,
  "",
  "| 分钟（从 0 起） | 受理/s | 确认/s | HTTP P99 ms | 待确认峰值 | 事务均值 ms | 提交均值 ms | DB 使用峰值 | DB 等待次数 | DB 等待总秒 | 确认 P99 s |",
  "|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|",
  ...minutes.map(r=>`| ${r.minute} | ${f(r.accepted_per_second)} | ${f(r.confirmed_per_second)} | ${f(r.http_p99_ms)} | ${r.pending_peak} | ${f(r.transaction_average_ms)} | ${f(r.commit_average_ms)} | ${r.pool_in_use_peak} | ${r.pool_wait_count} | ${f(r.pool_wait_seconds)} | ${f(r.confirmation_p99_seconds)} |`),
  "",
  "受理吞吐和 HTTP P99 来自 k6 每分钟成功受理请求；确认吞吐按轻量指标采样在整分钟边界插值后计算，避免慢 SQL 诊断耗时造成计时偏差。事务耗时取密集采样窗口的计数差值，阶段边界存在采样误差；确认 P99 为 Prometheus 直方图估计。恢复正常要求连续三个采样点回到突发前一分钟的待确认峰值以内。",
  "",
  `行锁等待共采样 ${result.lock_wait_samples} 条；请求索引分布 ${JSON.stringify(result.lock_wait_indexes)}，请求锁类型 ${JSON.stringify(result.lock_wait_modes)}。主要有 SQL 文本的等待对应订单从 queued 更新为 pending_payment，说明状态索引上的间隙锁竞争值得单独验证。部分阻塞 SQL 未能采到，不能把所有等待归因于某一个后台任务。`,
  "",
  `最终 ${verification.checks[0].orders} 单，订单/Inbox/Redis 与 MySQL 库存/看板核对${verification.passed?"通过":"失败"}。`,
  `持续写入期间对账差异读数：${JSON.stringify(result.reconciliation_mismatches)}。这些读数与停止输入后的最终库存核对分开判断。`,
  "",
  "live.ndjson 保存每 10 秒指标、约每分钟容器 CPU/内存和 MySQL I/O/压力快照；run/vus-400 保存更密集的指标、行锁等待 SQL 和最终队列状态。未出现的故障无法用这轮测试归因。",
];
await writeFile(join(root,"report.md"),lines.join("\n"));
console.log(JSON.stringify({http_requests:k6.http_reqs,dropped:k6.dropped_iterations,pending_peak:result.pending_peak,burst_recovery_seconds:result.burst_recovery_seconds,verified:verification.passed}));
