import { readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const root = resolve(process.argv[2]);
const read = async (path) => JSON.parse((await readFile(path, "utf8")).replace(/^\uFEFF/, ""));
const rows = [];
for (const vus of [100, 200, 500]) {
  const path = join(root, `vus-${vus}`);
  const run = await read(join(path, "run-summary.json"));
  const samples = await read(join(path, "lifecycle-samples.json"));
  const first = samples[0];
  const last = samples.at(-1);
  const seconds = (Date.parse(last.timestamp) - Date.parse(first.timestamp)) / 1000;
  const delta = (key) => (last.prometheus[key] || 0) - (first.prometheus[key] || 0);
  const confirmed = delta('ticket_order_consumer_transaction_duration_seconds_count{result="success"}');
  const stages = {};
  for (const stage of ["commit", "order_lock", "order_items_read", "tier_bucket_update", "rush_bucket_update"]) {
    const count = delta(`ticket_order_consumer_stage_duration_seconds_count{stage="${stage}"}`);
    stages[stage] = count ? 1000 * delta(`ticket_order_consumer_stage_duration_seconds_sum{stage="${stage}"}`) / count : null;
  }
  rows.push({
    ...run,
    sampling_window_seconds: seconds,
    confirmed_in_sampling_window: confirmed,
    confirmation_rate_in_sampling_window: confirmed / seconds,
    outbox_peak: Math.max(...samples.map((s) => s.prometheus.ticket_order_outbox_pending_rows || 0)),
    pool_in_use_peak: Math.max(...samples.map((s) => s.prometheus.go_sql_db_in_use || 0)),
    pool_wait_count_delta: delta("go_sql_db_wait_count"),
    pool_wait_seconds_delta: delta("go_sql_db_wait_duration_seconds"),
    consumer_stage_average_ms: stages,
    measured_at: first.timestamp,
    latency_threshold_passed: run.p99_ms < 1000,
  });
}
await writeFile(join(root, "analysis.json"), JSON.stringify(rows, null, 2) + "\n");
const number = (n) => Number(n).toFixed(1);
const report = [
  "# 下单链路压测",
  "",
  "后端 2 CPU / 1GB，MySQL 2 CPU / 2GB，Redis 1 CPU / 512MB，RabbitMQ 1 CPU / 1GB，Elasticsearch 1 CPU / 1GB。",
  "MySQL redo/binlog 双 1、binlog 开启；32 个库存桶、6 个 Consumer、4 个 Outbox 发布器，共享 100 个数据库连接。",
  "每档 30 秒纯秒杀请求；10000 个测试用户轮换，支付期限 15 分钟。",
  "",
  "| 并发 | 受理单/秒 | P99 ms | 请求成功率 | Outbox 采样峰值 | 采样窗口确认单/秒 | 停压后排空秒 | 最终确认单数 |",
  "|---:|---:|---:|---:|---:|---:|---:|---:|",
  ...rows.map((r) => `| ${r.vus} | ${number(r.http_req_rate)} | ${number(r.p99_ms)} | ${number(r.http_success_rate * 100)}% | ${r.outbox_peak} | ${number(r.confirmation_rate_in_sampling_window)} | ${number(r.drain_seconds)} | ${r.order_status_after.pending_payment || 0} |`),
  "",
  "接口成功表示 queued 已落库，不代表 Consumer 已确认。确认速率来自 Prometheus 累计计数差除以采样时间；采样窗口包括压测工具启动与停止检查，长度记录在 analysis.json，不能当成稳定容量。",
  "Outbox 与 MQ 峰值都是离散采样值。排空耗时包含轮询和三次空队列确认时间。",
  "本次是当前版本的短时容量测量，没有同条件改造前对照，不能据此声称改造提升百分比。",
  `脚本的延迟阈值为 P99 < 1000ms；未通过的并发档位：${rows.filter((r) => !r.latency_threshold_passed).map((r) => r.vus).join("、") || "无"}。请求成功率和延迟阈值分别判断。`,
  "",
  "| 并发 | 连接占用峰值 | 连接等待次数增量 | Consumer commit 平均 ms | tier 更新平均 ms | rush 更新平均 ms |",
  "|---:|---:|---:|---:|---:|---:|",
  ...rows.map((r) => `| ${r.vus} | ${r.pool_in_use_peak} | ${r.pool_wait_count_delta} | ${number(r.consumer_stage_average_ms.commit)} | ${number(r.consumer_stage_average_ms.tier_bucket_update)} | ${number(r.consumer_stage_average_ms.rush_bucket_update)} |`),
  "",
  "原始数据保存在各 vus-* 目录；库存核对与看板检查结果另见 verification.json。",
];
await writeFile(join(root, "report.md"), report.join("\n") + "\n");
console.log(JSON.stringify(rows.map(({ vus, http_req_rate, p99_ms, confirmation_rate_in_sampling_window, drain_seconds }) => ({ vus, http_req_rate, p99_ms, confirmation_rate_in_sampling_window, drain_seconds })), null, 2));
