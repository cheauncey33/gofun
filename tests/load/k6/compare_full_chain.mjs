import { readFile, writeFile, mkdir } from "node:fs/promises";
import { resolve, join, basename } from "node:path";

const output = resolve(process.argv[2]);
const read = async (path) => JSON.parse((await readFile(path, "utf8")).replace(/^\uFEFF/, ""));
const rows = [];
for (const input of process.argv.slice(3)) {
  const root = resolve(input);
  const summary = await read(join(root, "baseline-summary.json"));
  for (const run of Array.isArray(summary) ? summary : [summary]) {
    const samples = await read(join(root, `vus-${run.vus}`, "lifecycle-samples.json"));
    const first = samples[0], last = samples.at(-1);
    const httpEnd = samples.filter((s) => s.phase === "http").at(-1);
    const delta = (key) => (last.prometheus[key] || 0) - (first.prometheus[key] || 0);
    const seconds = (Date.parse(last.timestamp) - Date.parse(first.timestamp)) / 1000;
    const meanStage = (stage) => {
      const count = delta(`ticket_order_consumer_stage_duration_seconds_count{stage="${stage}"}`);
      return count ? 1000 * delta(`ticket_order_consumer_stage_duration_seconds_sum{stage="${stage}"}`) / count : null;
    };
    rows.push({
      case: basename(root), source: root, ...run,
      sampling_seconds: seconds,
      confirmation_rate: delta('ticket_order_consumer_transaction_duration_seconds_count{result="success"}') / seconds,
      outbox_peak: Math.max(...samples.map((s) => s.prometheus.ticket_order_outbox_pending_rows || 0)),
      total_backlog_peak: Math.max(...samples.map((s) => (s.prometheus.ticket_order_outbox_pending_rows || 0) + (s.rabbitmq.work_queue_total || 0))),
      http_last_backlog: (httpEnd.prometheus.ticket_order_outbox_pending_rows || 0) + (httpEnd.rabbitmq.work_queue_total || 0),
      accepted_to_confirmed_average_seconds: delta("ticket_order_accepted_to_pending_payment_duration_seconds_sum") / delta("ticket_order_accepted_to_pending_payment_duration_seconds_count"),
      commit_average_ms: meanStage("commit"),
      pool_waits: Object.fromEntries(["http", "worker"].map((pool) => [pool, delta(`go_sql_db_pool_wait_count{pool="${pool}"}`)])),
    });
  }
}
await mkdir(output, { recursive: true });
await writeFile(join(output, "comparison.json"), JSON.stringify(rows, null, 2) + "\n");
const n = (value) => Number(value).toFixed(1);
await writeFile(join(output, "comparison.md"), [
  "# 下单并发优化对照",
  "",
  "后端 2 CPU / 1GB，MySQL 2 CPU / 2GB，32 库存桶，MySQL redo/binlog 双 1；同一隔离测试栈依次运行，每轮新建活动。突发测试均为 200 并发、20 秒、10000 测试用户；steady 命名的测试为固定速率 200 单/秒、60 秒。",
  "",
  "| 方案 | Consumer | 受理单/秒 | P99 ms | 完整采样确认单/秒 | Outbox+MQ 峰值 | 停压后检查秒 | commit 平均 ms |",
  "|---|---:|---:|---:|---:|---:|---:|---:|",
  ...rows.map((r) => `| ${r.case} | ${r.consumer_workers} | ${n(r.http_req_rate)} | ${n(r.p99_ms)} | ${n(r.confirmation_rate)} | ${r.total_backlog_peak} | ${n(r.drain_seconds)} | ${n(r.commit_average_ms)} |`),
  "",
  "确认速率按完整生命周期的累计确认计数差除以采样时间计算，包括启动和停止检查；不是长期最大容量。停压后检查时间包含三次空队列确认。峰值是离散采样值。",
  "各方案未随机排序，数据库数据量随测试增加；小幅差异需重复验证。成功响应表示已受理，不代表已确认。正确性核对见各目录 verification.json。",
  "",
].join("\n"));
console.log(JSON.stringify(rows.map(({ case: name, confirmation_rate, http_req_rate, p99_ms, drain_seconds }) => ({ name, confirmation_rate, http_req_rate, p99_ms, drain_seconds })), null, 2));
