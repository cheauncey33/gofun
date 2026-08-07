/**
 * 多热点抢票压测扫描：
 * - 不打同一票档/活动；每轮按 HOTSPOTS 在 DB 直插 N 个独立活动+票档+抢票。
 * - 总请求数 TOTAL_ORDERS 均分到各热点（user i → campaign[i % H]）。
 * - 扫描 HOTSPOT_STEPS（如 1,2,4,8,16），对比 MQ 追平与行锁等待。
 *
 * 示例：
 *   $env:BASE_URL='http://127.0.0.1:18080/api/v1'
 *   $env:INVENTORY_BUCKETS_ENABLED='true'   # 与后端一致
 *   $env:HOTSPOT_STEPS='1,2,4,8'
 *   $env:TOTAL_ORDERS='1500'
 *   $env:RUNS='3'
 *   node tests/load/ticket_rush_multi_hotspot.mjs
 */
import { randomUUID } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  Metrics,
  cfg,
  ensureUser,
  httpJson,
  sleep,
} from "./lib/load_common.mjs";
import { ResourceSampler } from "./lib/resource_sampler.mjs";
import { seedMultiHotspotCampaigns } from "./lib/multi_hotspot_seed.mjs";

const hotspotSteps = String(process.env.HOTSPOT_STEPS || "1,2,4,8")
  .split(",")
  .map(Number)
  .filter((value) => Number.isInteger(value) && value > 0);
const totalOrders = Number(process.env.TOTAL_ORDERS || 1500);
const runs = Number(process.env.RUNS || 3);
const waveSize = Number(process.env.WAVE_SIZE || 50);
const pollSeconds = Number(process.env.POLL_SECONDS || 120);
const cooldownSeconds = Number(process.env.COOLDOWN_SECONDS || 5);
const setupConcurrency = Number(process.env.SETUP_CONCURRENCY || 20);
const quotaBufferRatio = Number(process.env.QUOTA_BUFFER_RATIO || 0.15);
const runLabel = process.env.RUN_LABEL || `multi-hotspot-${Date.now()}`;
const resultsDir =
  process.env.RESULTS_DIR || join("tests", "load", "results", runLabel);
const metricsURL = process.env.METRICS_URL || "http://127.0.0.1:18080/metrics";

if (hotspotSteps.length === 0) throw new Error("HOTSPOT_STEPS must contain positive integers");
if (!Number.isInteger(totalOrders) || totalOrders <= 0) {
  throw new Error("TOTAL_ORDERS must be positive");
}
if (!Number.isInteger(runs) || runs <= 0) throw new Error("RUNS must be positive");

await mkdir(resultsDir, { recursive: true });

const maxUsers = totalOrders;
const setupMetrics = new Metrics("multi_hotspot_user_setup");
const users = [];
console.error(`preparing ${maxUsers} users...`);
for (let offset = 0; offset < maxUsers; offset += setupConcurrency) {
  const indexes = Array.from(
    { length: Math.min(setupConcurrency, maxUsers - offset) },
    (_, idx) => offset + idx,
  );
  const batch = await Promise.all(
    indexes.map(async (index) => {
      const username = `${cfg.loadUserPrefix}mh_${index}`;
      const token = await ensureUser(username, cfg.loadPassword, setupMetrics);
      return { username, token };
    }),
  );
  users.push(...batch);
}
await sleep(cooldownSeconds * 1000);

const aggregate = {
  scenario: "ticket_rush_multi_hotspot",
  run_label: runLabel,
  created_at: new Date().toISOString(),
  config: {
    base_url: cfg.baseUrl,
    hotspot_steps: hotspotSteps,
    total_orders: totalOrders,
    runs,
    wave_size: waveSize,
    poll_seconds: pollSeconds,
    quota_buffer_ratio: quotaBufferRatio,
    inventory_buckets_enabled: process.env.INVENTORY_BUCKETS_ENABLED || "",
    inventory_bucket_count: process.env.INVENTORY_BUCKET_COUNT || "",
    order_consumer_worker_count: process.env.ORDER_CONSUMER_WORKER_COUNT || "",
    order_outbox_write_mode: process.env.ORDER_OUTBOX_WRITE_MODE || "",
  },
  setup: setupMetrics.summary(),
  results: [],
};

for (const hotspots of hotspotSteps) {
  for (let round = 1; round <= runs; round += 1) {
    const label = `${runLabel}-h${hotspots}-r${round}`;
    const runDir = join(resultsDir, `h${hotspots}`, `r${round}`);
    await mkdir(runDir, { recursive: true });
    console.error(`starting ${label}...`);

    const perCampaignLoad = Math.ceil(totalOrders / hotspots);
    const quotaPerCampaign =
      perCampaignLoad + Math.max(20, Math.ceil(perCampaignLoad * quotaBufferRatio));

    const seeded = await seedMultiHotspotCampaigns({
      count: hotspots,
      quotaPerCampaign,
      perUserLimit: 1,
      label,
    });
    await writeFile(
      join(runDir, "seed.json"),
      `${JSON.stringify(seeded, null, 2)}\n`,
    );

    const runUsers = users.slice(0, totalOrders).map((user, index) => ({
      ...user,
      campaignID: seeded.campaigns[index % hotspots].campaignID,
      hotspotIndex: index % hotspots,
      orderID: null,
      status: null,
    }));

    const execute = new Metrics("multi_hotspot_execute");
    const poll = new Metrics("multi_hotspot_poll");
    const metricsBefore = await fetch(metricsURL).then((res) => res.text());
    await writeFile(join(runDir, "metrics-before.prom"), metricsBefore);

    const sampler = new ResourceSampler({
      containers: String(process.env.MONITOR_CONTAINERS || "")
        .split(",")
        .map((value) => value.trim()),
      mysqlContainer: process.env.MYSQL_CONTAINER || "",
      redisContainer: process.env.REDIS_CONTAINER || "",
      rabbitContainer: process.env.RABBITMQ_CONTAINER || "",
      intervalMs: Number(process.env.RESOURCE_SAMPLE_INTERVAL_MS || 1000),
    });
    await sampler.start();

    const started = performance.now();
    for (let offset = 0; offset < runUsers.length; offset += waveSize) {
      const wave = runUsers.slice(offset, offset + waveSize);
      await Promise.all(
        wave.map(async (user) => {
          const { data, res } = await httpJson(
            "POST",
            `/rush-sales/${user.campaignID}/execute`,
            {
              token: user.token,
              label: "rush_execute",
              metrics: execute,
              okStatuses: [200, 400, 409, 429],
              headers: { "X-Idempotency-Key": randomUUID() },
              body: {
                quantity: 1,
                contact_name: "多热点压测用户",
                contact_phone: "13900139000",
                terms_accepted: true,
                attendees: [],
              },
            },
          );
          if (res?.status === 200) {
            user.orderID = String(data?.data?.order_id || data?.data?.id || "");
            user.status = data?.data?.status || "queued";
          }
        }),
      );
    }
    const executeSeconds = (performance.now() - started) / 1000;

    const successes = runUsers.filter((user) => user.orderID);
    const pollStarted = performance.now();
    const deadline = pollStarted + pollSeconds * 1000;
    while (performance.now() < deadline) {
      const stillQueued = successes.filter(
        (user) => !user.status || user.status === "queued",
      );
      if (stillQueued.length === 0) break;
      await Promise.all(
        stillQueued.slice(0, waveSize).map(async (user) => {
          const { data, ok } = await httpJson("GET", `/orders/${user.orderID}`, {
            token: user.token,
            label: "order_poll",
            metrics: poll,
            okStatuses: [200],
          });
          if (ok) user.status = data?.data?.status || user.status;
        }),
      );
      await sleep(200);
    }
    const drainSeconds = (performance.now() - pollStarted) / 1000;
    const resources = await sampler.stop();

    const metricsAfter = await fetch(metricsURL).then((res) => res.text());
    await writeFile(join(runDir, "metrics-after.prom"), metricsAfter);
    await writeFile(
      join(runDir, "resources.json"),
      `${JSON.stringify(resources, null, 2)}\n`,
    );

    const byHotspot = {};
    for (const user of runUsers) {
      const key = String(user.hotspotIndex);
      if (!byHotspot[key]) {
        byHotspot[key] = {
          campaign_id: user.campaignID,
          attempts: 0,
          success: 0,
          still_queued: 0,
        };
      }
      byHotspot[key].attempts += 1;
      if (user.orderID) byHotspot[key].success += 1;
      if (user.status === "queued") byHotspot[key].still_queued += 1;
    }

    const executeSummary = execute.summary();
    const runSummary = {
      label,
      hotspots,
      round,
      total_orders: totalOrders,
      quota_per_campaign: quotaPerCampaign,
      campaign_ids: seeded.campaigns.map((c) => c.campaignID),
      execute_seconds: roundNumber(executeSeconds),
      drain_seconds: roundNumber(drainSeconds),
      success_orders: successes.length,
      success_rate: roundNumber(successes.length / totalOrders),
      success_qps: roundNumber(successes.length / Math.max(executeSeconds, 0.001)),
      still_queued: successes.filter((user) => user.status === "queued").length,
      by_hotspot: byHotspot,
      execute: executeSummary,
      poll: poll.summary(),
      mq_metrics: {
        published_delta:
          metricValue(metricsAfter, "mq_messages_published_total") -
          metricValue(metricsBefore, "mq_messages_published_total"),
        consumed_success_delta:
          metricLabeled(metricsAfter, "mq_messages_consumed_total", "success") -
          metricLabeled(metricsBefore, "mq_messages_consumed_total", "success"),
      },
      resources: {
        containers: resources.containers,
        mysql: resources.mysql,
        redis: resources.redis,
        rabbitmq: resources.rabbitmq,
      },
    };
    aggregate.results.push(runSummary);
    await writeFile(join(runDir, "load.json"), `${JSON.stringify(runSummary, null, 2)}\n`);
    await writeAggregate();
    console.error(
      `${label}: success=${successes.length}/${totalOrders} qps=${runSummary.success_qps} ` +
        `p99=${executeSummary.latency_ms.p99}ms drain=${runSummary.drain_seconds}s ` +
        `locks=${resources.mysql?.row_lock_waits_delta ?? "n/a"}`,
    );
    await sleep(cooldownSeconds * 1000);
  }
}

await writeAggregate();
const summaryTable = buildSummaryTable(aggregate.results);
await writeFile(
  join(resultsDir, "multi-hotspot-summary.md"),
  renderMarkdown(aggregate, summaryTable),
);
console.log(JSON.stringify({ ...aggregate, summary_table: summaryTable }, null, 2));

async function writeAggregate() {
  await writeFile(
    join(resultsDir, "multi-hotspot-summary.json"),
    `${JSON.stringify(aggregate, null, 2)}\n`,
  );
}

function buildSummaryTable(results) {
  const byH = new Map();
  for (const row of results) {
    const list = byH.get(row.hotspots) || [];
    list.push(row);
    byH.set(row.hotspots, list);
  }
  const table = [];
  for (const hotspots of [...byH.keys()].sort((a, b) => a - b)) {
    const rows = byH.get(hotspots);
    const med = (picker) => median(rows.map(picker));
    table.push({
      hotspots,
      runs: rows.length,
      success_qps_median: med((r) => r.success_qps),
      execute_p99_median: med((r) => r.execute.latency_ms.p99),
      drain_seconds_median: med((r) => r.drain_seconds),
      row_lock_waits_median: med((r) => r.resources?.mysql?.row_lock_waits_delta ?? 0),
      success_rate_median: med((r) => r.success_rate),
    });
  }
  return table;
}

function renderMarkdown(agg, table) {
  const lines = [
    `# 多热点抢票压测扫描：${agg.run_label}`,
    "",
    `- TOTAL_ORDERS=${agg.config.total_orders}`,
    `- HOTSPOT_STEPS=${agg.config.hotspot_steps.join(",")}`,
    `- RUNS=${agg.config.runs}`,
    `- buckets_enabled=${agg.config.inventory_buckets_enabled || "(unset)"}`,
    "",
    "| 热点数 | 入口 QPS 中位 | execute p99 中位 | MQ 追平中位 | row_lock_waits 中位 | 成功率中位 |",
    "| ---: | ---: | ---: | ---: | ---: | ---: |",
  ];
  for (const row of table) {
    lines.push(
      `| ${row.hotspots} | ${row.success_qps_median} | ${row.execute_p99_median}ms | ${row.drain_seconds_median}s | ${row.row_lock_waits_median} | ${row.success_rate_median} |`,
    );
  }
  lines.push("");
  return `${lines.join("\n")}\n`;
}

function median(values) {
  const sorted = values.slice().sort((a, b) => a - b);
  if (sorted.length === 0) return 0;
  const mid = Math.floor(sorted.length / 2);
  const value =
    sorted.length % 2 === 0 ? (sorted[mid - 1] + sorted[mid]) / 2 : sorted[mid];
  return roundNumber(value);
}

function metricValue(text, name) {
  const match = text.match(
    new RegExp(`^${name}(?:\\{[^}]*\\})?\\s+([0-9.eE+-]+)$`, "m"),
  );
  return match ? Number(match[1]) : 0;
}

function metricLabeled(text, name, result) {
  const match = text.match(
    new RegExp(
      `^${name}\\{[^}]*result="${result}"[^}]*\\}\\s+([0-9.eE+-]+)$`,
      "m",
    ),
  );
  return match ? Number(match[1]) : 0;
}

function roundNumber(value) {
  return Number(Number(value).toFixed(2));
}
