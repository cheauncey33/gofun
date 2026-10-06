import { readFile, writeFile, readdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import assert from "node:assert/strict";

const root = resolve(process.argv[2]);
const read = async (path) => JSON.parse((await readFile(path, "utf8")).replace(/^\uFEFF/, ""));
const acceptedKey = 'http_requests_total{method="POST",path="/api/v1/rush-sales/:id/execute",status="200"}';
const confirmedKey = "ticket_order_accepted_to_pending_payment_duration_seconds_count";
const rows = [];

function quantile(first, last, prefix, q) {
  const buckets = Object.keys(last.prometheus).filter((key) => key.startsWith(`${prefix}_bucket{`))
    .map((key) => ({ upper: Number(key.match(/le="([^"]+)"/)[1].replace("+Inf", "Infinity")),
      count: last.prometheus[key] - (first.prometheus[key] || 0) })).sort((a, b) => a.upper - b.upper);
  if (!buckets.length || buckets.at(-1).count === 0) return null;
  const rank = buckets.at(-1).count * q;
  let lower = 0, previous = 0;
  for (const bucket of buckets) {
    if (bucket.count >= rank) {
      if (!Number.isFinite(bucket.upper)) return lower;
      return lower + (bucket.upper - lower) * (rank - previous) / (bucket.count - previous);
    }
    lower = bucket.upper; previous = bucket.count;
  }
}

for (const directory of await readdir(root, { withFileTypes: true })) {
  if (!directory.isDirectory() || !/^(repeat-)?(baseline|current)-\d+$/.test(directory.name)) continue;
  const path = join(root, directory.name);
  // A running stage has no variant.json until its drain and verification finish.
  const files = await readdir(path);
  if (!files.includes("variant.json")) continue;
  const variant = await read(join(path, "variant.json"));
  const runData = await read(join(path, "baseline-summary.json"));
  const run = Array.isArray(runData) ? runData[0] : runData;
  const diagnostic = await read(join(path, "vus-200", "diagnostic-summary.json"));
  const samples = await read(join(path, "vus-200", "lifecycle-samples.json"));
  const verification = await read(join(path, "verification.json"));
  const initial = samples[0], final = samples.at(-1);
  const http = samples.filter((s) => s.phase === "http" && Object.keys(s.prometheus).length);
  const active = http.filter((s) => (s.prometheus[acceptedKey] || 0) > (initial.prometheus[acceptedKey] || 0));
  assert.ok(active.length > 3, "HTTP sampling window missing");
  // Drop the first 30 seconds and last 5 seconds of observed active sampling.
  const startTime = Date.parse(active[0].timestamp) + 30000;
  const endTime = Date.parse(active.at(-1).timestamp) - 5000;
  const steady = active.filter((s) => Date.parse(s.timestamp) >= startTime && Date.parse(s.timestamp) <= endTime);
  assert.ok(steady.length > 3, "Steady sampling window missing");
  const first = steady[0], last = steady.at(-1);
  const seconds = (Date.parse(last.timestamp) - Date.parse(first.timestamp)) / 1000;
  const delta = (key, a = first, b = last) => (b.prometheus[key] || 0) - (a.prometheus[key] || 0);
  const backlog = (s) => Math.max(0, delta(acceptedKey, initial, s) - delta(confirmedKey, initial, s));
  const stageMean = (stage) => delta(`ticket_order_consumer_stage_duration_seconds_sum{stage="${stage}"}`)
    / delta(`ticket_order_consumer_stage_duration_seconds_count{stage="${stage}"}`) * 1000;
  const confirmed = delta(confirmedKey, initial, final);
  assert.equal(confirmed, verification.checks[0].confirmed, "Confirmation metric disagrees with database");
  const waits = delta("go_sql_db_wait_count");
  const k6 = diagnostic.summary;
  const row = {
    case: directory.name, ...variant,
    steady_start: first.timestamp, steady_end: last.timestamp, steady_seconds: seconds,
    accepted_per_second: delta(acceptedKey) / seconds,
    confirmed_per_second: delta(confirmedKey) / seconds,
    outbox_publish_per_second: delta("mq_messages_published_total") / seconds,
    pending_at_steady_start: backlog(first), pending_at_steady_end: backlog(last),
    backlog_growth_per_second: (backlog(last) - backlog(first)) / seconds,
    pending_at_http_end: backlog(active.at(-1)), pending_peak: Math.max(...active.map(backlog)),
    http_p95_ms: k6.p95_ms, http_p99_ms: k6.p99_ms,
    http_requests: k6.http_reqs, http_completed_per_second: k6.http_req_rate,
    success_rate: k6.rush_execute_success_rate, dropped_iterations: k6.dropped_iterations,
    k6_exit_code: diagnostic.exit_code,
    accepted_to_confirmed_average_seconds: delta("ticket_order_accepted_to_pending_payment_duration_seconds_sum", initial, final) / confirmed,
    accepted_to_confirmed_p99_seconds: quantile(initial, final, "ticket_order_accepted_to_pending_payment_duration_seconds", .99),
    consumer_transaction_average_ms: delta('ticket_order_consumer_transaction_duration_seconds_sum{result="success"}')
      / delta('ticket_order_consumer_transaction_duration_seconds_count{result="success"}') * 1000,
    commit_average_ms: stageMean("commit"), order_lock_average_ms: stageMean("order_lock"),
    items_average_ms: stageMean("order_items_read"), tier_update_average_ms: stageMean("tier_bucket_update"),
    rush_update_average_ms: stageMean("rush_bucket_update"),
    pool_in_use_peak: Math.max(...steady.map((s) => s.prometheus.go_sql_db_in_use)),
    consumer_count_observed: Math.max(...steady.map((s) => s.prometheus['mq_queue_consumers{queue="fuchang.it.order.queue"}'])),
    pool_wait_count: waits, pool_wait_seconds: delta("go_sql_db_wait_duration_seconds"),
    pool_mean_wait_ms: waits ? delta("go_sql_db_wait_duration_seconds") * 1000 / waits : 0,
    app_cpu_cores: delta("process_cpu_seconds_total") / seconds,
    lock_waits: last.mysql.innodb_row_lock_waits - first.mysql.innodb_row_lock_waits,
    lock_wait_ms: last.mysql.innodb_row_lock_time - first.mysql.innodb_row_lock_time,
    lock_wait_peak: Math.max(...steady.map((s) => s.mysql.innodb_row_lock_current_waits)),
    outbox_peak: Math.max(...active.map((s) => s.prometheus.ticket_order_outbox_pending_rows || 0)),
    mq_peak: Math.max(...active.map((s) => s.rabbitmq.work_queue_total)),
    drain_check_seconds: run.drain_seconds, confirmed_orders: confirmed,
    verification_passed: verification.passed,
  };
  assert.equal(row.consumer_count_observed, variant.consumers, "Observed MQ consumers disagree with variant settings");
  row.load_delivered = row.success_rate === 1 && row.dropped_iterations === 0 && row.accepted_per_second >= row.rate * .98;
  row.capacity_passed = row.load_delivered && row.http_p99_ms < 1000 && row.confirmed_per_second >= row.rate * .98
    && row.backlog_growth_per_second <= Math.max(1, row.rate * .01) && row.pending_at_http_end <= row.rate;
  rows.push(row);
}
rows.sort((a, b) => a.order - b.order || a.variant.localeCompare(b.variant));
const capacityNotes = ["baseline", "current"].map((variant) => {
  const tested = rows.filter((r) => r.variant === variant);
  const passed = tested.filter((r) => r.capacity_passed).map((r) => r.rate);
  const failed = tested.filter((r) => !r.capacity_passed).map((r) => r.rate);
  const inconsistent = passed.some((rate) => failed.includes(rate));
  return `${variant}：最高通过档 ${passed.length ? Math.max(...passed) : "尚无"} 单/秒，最低未通过档 ${failed.length ? Math.min(...failed) : "尚未测到"} 单/秒。${inconsistent ? "同档复测出现失败，不能认定该档为稳定容量。" : ""}`;
});
const before300 = rows.find((r) => r.variant === "baseline" && r.rate === 300);
const after300 = rows.find((r) => r.variant === "current" && r.rate === 300);
await writeFile(join(root, "analysis.json"), JSON.stringify(rows, null, 2) + "\n");
const f = (n) => n === null || !Number.isFinite(n) ? "—" : n.toFixed(1);
await writeFile(join(root, "report.md"), [
  "# 阶梯压测与优化前后对照", "",
  "单应用，后端 2 CPU / 1GB，MySQL 2 CPU / 2GB；共享 100 连接、32 库存桶、4 Publisher，redo/binlog 双 1。历史基线镜像 6 Consumer；当前镜像 12 Consumer，包含 Outbox 合并更新与 Consumer SQL 精简。镜像 ID 见 images.json。",
  "每档固定输入持续 120 秒，10000 用户、库存 200000，每轮新活动，前一轮排空后才开始。100/200/300/400 档交替执行 AB/BA，最后复测 200。每轮首次观测到请求后的前 30 秒及尾部 5 秒不计入持续吞吐窗口。",
  "",
  ...capacityNotes,
  ...rows.filter((r) => r.repeat).map((r) => `200 档复测 ${r.variant}：确认 ${f(r.confirmed_per_second)}/s，HTTP P99 ${f(r.http_p99_ms)}ms，丢弃 ${r.dropped_iterations} 次迭代，${r.capacity_passed ? "通过" : "未通过"}。`),
  ...(rows.some((r) => r.repeat && r.variant === "current" && !r.capacity_passed) ? ["当前版本初轮 200 档收益成立，但复测有延迟突刺、连接等待和输入丢弃，尚未证明稳定承载 200 单/秒。两轮都保留，不能只引用初轮的有利结果。"] : []),
  "",
  "| 版本/档位 | 稳态受理/s | 持续确认/s | 积压增长/s | 停压时未确认 | HTTP P99 ms | 确认 P99 估算 s | 丢弃迭代 | 容量条件 |",
  "|---|---:|---:|---:|---:|---:|---:|---:|---|",
  ...rows.map((r) => `| ${r.case} | ${f(r.accepted_per_second)} | ${f(r.confirmed_per_second)} | ${f(r.backlog_growth_per_second)} | ${r.pending_at_http_end} | ${f(r.http_p99_ms)} | ${f(r.accepted_to_confirmed_p99_seconds)} | ${r.dropped_iterations} | ${r.capacity_passed ? "通过" : r.load_delivered ? "超出" : "输入未完整送达"} |`),
  "",
  "容量条件：100% 成功、0 丢弃迭代、实际受理与确认达到目标的 98%，HTTP P99 < 1s，积压增长不超过 max(1,目标速率×1%)，停压时未确认数不超过一秒输入量。未满足表示超出本次验收条件，不表示订单丢失。",
  "未确认数由同窗口新增受理计数减去真实状态转换确认计数计算，避免把 Outbox 与 MQ 相加重复计数；短时采样存在观测时差。确认延迟来自完整受理至排空窗口的直方图插值 P99，尾部不会被停压截掉，区别于 HTTP 返回延迟。",
  "",
  "| 版本/档位 | 连接占用峰值 | 连接等待次数 | 平均连接等待 ms | 事务平均 ms | commit ms | 行锁等待次数 | 应用 CPU 核 | 排空检查 s |",
  "|---|---:|---:|---:|---:|---:|---:|---:|---:|",
  ...rows.map((r) => `| ${r.case} | ${r.pool_in_use_peak} | ${r.pool_wait_count} | ${f(r.pool_mean_wait_ms)} | ${f(r.consumer_transaction_average_ms)} | ${f(r.commit_average_ms)} | ${r.lock_waits} | ${f(r.app_cpu_cores)} | ${f(r.drain_check_seconds)} |`),
  "",
  "| 版本/档位 | Outbox 发布确认/s | Outbox 待发布峰值 | MQ 在途峰值 | 明细读取 ms | 库存桶更新 ms |",
  "|---|---:|---:|---:|---:|---:|",
  ...rows.map((r) => `| ${r.case} | ${f(r.outbox_publish_per_second)} | ${r.outbox_peak} | ${r.mq_peak} | ${f(r.items_average_ms)} | ${f(r.tier_update_average_ms + r.rush_update_average_ms)} |`),
  "",
  ...(before300 && after300 ? [
    `300 档限制点变化：基线发布 ${f(before300.outbox_publish_per_second)}/s、确认 ${f(before300.confirmed_per_second)}/s，Outbox 峰值 ${before300.outbox_peak}、MQ 峰值 ${before300.mq_peak}，积压主要留在发布侧。当前发布 ${f(after300.outbox_publish_per_second)}/s、确认 ${f(after300.confirmed_per_second)}/s，Outbox 峰值 ${after300.outbox_peak}、MQ 峰值 ${after300.mq_peak}，限制转到消费侧。`,
    `当前 12 Consumer 的成功事务平均 ${f(after300.consumer_transaction_average_ms)}ms，12×1000/事务耗时≈${f(12000 / after300.consumer_transaction_average_ms)} 单/秒，与实测 ${f(after300.confirmed_per_second)} 接近；提交约占 ${f(after300.commit_average_ms / after300.consumer_transaction_average_ms * 100)}%。连接池峰值 ${after300.pool_in_use_peak}/100、稳态等待 ${after300.pool_wait_count} 次，不支持把连接池打满作为这个档位的主因。增加 Consumer 是否有效还需要单变量对照，不能假定线性扩容。`,
    "",
  ] : []),
  `已完成 ${rows.length} 轮，确认 ${rows.reduce((n, r) => n + r.confirmed_orders, 0)} 单，订单/Inbox/库存/看板核对：${rows.every((r) => r.verification_passed) ? "全部通过" : "有失败"}。`,
  "这里只是当前单机、纯下单、两分钟各档的容量区间，不是长期容量或生产承诺。优化为整组对照，不能把提升归因于一个 SQL；数据量逐轮增长、缓存和采样开销均可能影响结果，200 档复测用于检查波动。排空检查包含轮询和三次空队列检查。",
].join("\n") + "\n");
console.log(JSON.stringify(rows.at(-1)));
