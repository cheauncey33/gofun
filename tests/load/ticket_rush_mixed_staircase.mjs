/**
 * 抢票混合负载并发阶梯压测�? *
 * 每位用户先提交一次首次下单；随后按固定随机种子分配为�? * - 70% 使用新幂等键重复提交，应命中单用户限购拒绝；
 * - 30% 使用原幂等键重试，应返回原订单而不是创建新订单�? *
 * 请求启动和重试间隔带有可复现抖动。CONCURRENCY_STEPS 是每档最大并发，
 * 不把随机数当作不可重现的“随机并发”�? */
import { randomUUID } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { Metrics, cfg, ensureUser, httpJson, sleep } from "./lib/load_common.mjs";
import { ResourceSampler } from "./lib/resource_sampler.mjs";
import { seedMultiHotspotCampaigns } from "./lib/multi_hotspot_seed.mjs";

const concurrencySteps = parsePositiveInts(process.env.CONCURRENCY_STEPS || "20,40,60,80");
const runs = positiveInt(process.env.RUNS, 3);
const successUsers = positiveInt(process.env.SUCCESS_USERS, 1000);
const limitRepeatRatio = ratio(process.env.LIMIT_REPEAT_RATIO, 0.35);
const idempotentRetryRatio = ratio(process.env.IDEMPOTENT_RETRY_RATIO, 0.15);
const primaryPurchaseRatio = 1 - limitRepeatRatio - idempotentRetryRatio;
const startJitterMs = nonNegativeInt(process.env.START_JITTER_MS, 500);
const retryJitterMs = nonNegativeInt(process.env.RETRY_JITTER_MS, 120);
const pollSeconds = positiveInt(process.env.POLL_SECONDS, 120);
const cooldownSeconds = nonNegativeInt(process.env.COOLDOWN_SECONDS, 5);
const setupConcurrency = positiveInt(process.env.SETUP_CONCURRENCY, 20);
const baseSeed = positiveInt(process.env.SEED, 20260731);
const runLabel = process.env.RUN_LABEL || `mixed-rush-${Date.now()}`;
const resultsDir = process.env.RESULTS_DIR || join("tests", "load", "results", runLabel);
const metricsURL = process.env.METRICS_URL || "http://127.0.0.1:18080/metrics";
const monitorContainers = String(
  process.env.MONITOR_CONTAINERS ||
  "gofun-capacity-backend-load,gofun-capacity-mysql-1,gofun-capacity-redis-1,gofun-capacity-rabbitmq-1",
).split(",").map((value) => value.trim()).filter(Boolean);

if (concurrencySteps.length === 0) throw new Error("CONCURRENCY_STEPS must contain positive integers");
if (primaryPurchaseRatio <= 0 || limitRepeatRatio + idempotentRetryRatio > primaryPurchaseRatio) {
  throw new Error("follow-up ratios must be no greater than primary purchase ratio");
}

await mkdir(resultsDir, { recursive: true });

const setupMetrics = new Metrics("mixed_rush_user_setup");
const users = [];
console.error(`preparing ${successUsers} users...`);
for (let offset = 0; offset < successUsers; offset += setupConcurrency) {
  const indexes = Array.from({ length: Math.min(setupConcurrency, successUsers - offset) }, (_, index) => offset + index);
  const batch = await Promise.all(indexes.map(async (index) => ({
    username: `${cfg.loadUserPrefix}mixed_${index}`,
    token: await ensureUser(`${cfg.loadUserPrefix}mixed_${index}`, cfg.loadPassword, setupMetrics),
  })));
  users.push(...batch);
}
await sleep(cooldownSeconds * 1000);

const aggregate = {
  scenario: "ticket_rush_mixed_staircase",
  run_label: runLabel,
  created_at: new Date().toISOString(),
  workload: {
    primary_purchase_ratio: 0.5,
    limit_repeat_ratio: limitRepeatRatio,
    idempotent_retry_ratio: idempotentRetryRatio,
    success_users: successUsers,
    total_requests_per_run: Math.floor(successUsers / primaryPurchaseRatio),
    request_start_jitter_ms: startJitterMs,
    retry_jitter_ms: retryJitterMs,
    seed: baseSeed,
  },
  config: {
    base_url: cfg.baseUrl,
    concurrency_steps: concurrencySteps,
    runs,
    inventory_buckets_enabled: process.env.INVENTORY_BUCKETS_ENABLED || "",
    inventory_bucket_count: process.env.INVENTORY_BUCKET_COUNT || "",
    order_consumer_worker_count: process.env.ORDER_CONSUMER_WORKER_COUNT || "",
    order_consumer_prefetch_count: process.env.ORDER_CONSUMER_PREFETCH_COUNT || "",
  },
  setup: setupMetrics.summary(),
  results: [],
};

for (const concurrency of concurrencySteps) {
  for (let round = 1; round <= runs; round += 1) {
    const label = `${runLabel}-c${concurrency}-r${round}`;
    const runDir = join(resultsDir, `c${concurrency}`, `r${round}`);
    await mkdir(runDir, { recursive: true });
    const seed = baseSeed + concurrency * 100 + round;
    const random = mulberry32(seed);
    console.error(`starting ${label}...`);

    const seeded = await seedMultiHotspotCampaigns({
      count: 1,
      quotaPerCampaign: successUsers,
      perUserLimit: 1,
      label,
    });
    const campaignID = seeded.campaigns[0].campaignID;
    const flows = buildFlows(users, limitRepeatRatio, idempotentRetryRatio, primaryPurchaseRatio, random);
    await writeFile(join(runDir, "workload.json"), `${JSON.stringify({ seed, campaign_id: campaignID, flows: flows.map(({ token, ...flow }) => flow) }, null, 2)}\n`);

    const execute = new Metrics("mixed_rush_execute");
    const poll = new Metrics("mixed_rush_poll");
    const metricsBefore = await fetch(metricsURL).then((response) => response.text());
    await writeFile(join(runDir, "metrics-before.prom"), metricsBefore);
    const sampler = new ResourceSampler({
      containers: monitorContainers,
      mysqlContainer: process.env.MYSQL_CONTAINER || "",
      redisContainer: process.env.REDIS_CONTAINER || "",
      rabbitContainer: process.env.RABBITMQ_CONTAINER || "",
      intervalMs: Number(process.env.RESOURCE_SAMPLE_INTERVAL_MS || 1000),
    });
    await sampler.start();

    const started = performance.now();
    const outcome = newOutcome();
    await runWithConcurrency(flows, concurrency, async (flow) => {
      await sleep(flow.startDelayMs);
      const firstKey = randomUUID();
      const first = await executeRush(flow.token, campaignID, firstKey, execute, "primary_purchase");
      flow.orderID = receiptOrderID(first.data);
      countPrimary(outcome, first.res?.status, flow.orderID);
      if (!flow.followup) return;

      await sleep(flow.retryDelayMs);
      const key = flow.followup === "limit_repeat" ? randomUUID() : firstKey;
      const repeat = await executeRush(flow.token, campaignID, key, execute, flow.followup);
      const repeatOrderID = receiptOrderID(repeat.data);
      countFollowup(outcome, flow.followup, repeat.res?.status, flow.orderID, repeatOrderID);
    });
    const executeSeconds = (performance.now() - started) / 1000;

    const created = flows.filter((flow) => flow.orderID);
    const pollStarted = performance.now();
    const deadline = pollStarted + pollSeconds * 1000;
    while (performance.now() < deadline) {
      const queued = created.filter((flow) => !flow.status || flow.status === "queued");
      if (queued.length === 0) break;
      await Promise.all(queued.slice(0, concurrency).map(async (flow) => {
        const { data, ok } = await httpJson("GET", `/orders/${flow.orderID}`, {
          token: flow.token,
          label: "order_poll",
          metrics: poll,
          okStatuses: [200],
        });
        if (ok) flow.status = data?.data?.status || flow.status;
      }));
      await sleep(200);
    }
    const drainSeconds = (performance.now() - pollStarted) / 1000;
    const resources = await sampler.stop();
    const metricsAfter = await fetch(metricsURL).then((response) => response.text());
    await writeFile(join(runDir, "metrics-after.prom"), metricsAfter);
    await writeFile(join(runDir, "resources.json"), `${JSON.stringify(resources, null, 2)}\n`);

    const totalRequests = outcome.primary_attempts + outcome.limit_repeat_attempts + outcome.idempotent_retry_attempts;
    const runSummary = {
      label,
      concurrency,
      round,
      seed,
      campaign_id: campaignID,
      total_requests: totalRequests,
      execute_seconds: roundNumber(executeSeconds),
      total_qps: roundNumber(totalRequests / Math.max(executeSeconds, 0.001)),
      execute: execute.summary(),
      outcome,
      correctness: {
        expected_primary_successes: successUsers,
        primary_successes_match: outcome.primary_successes === successUsers,
        limit_rejections_match: outcome.limit_repeat_rejected === outcome.limit_repeat_attempts,
        idempotent_retries_match: outcome.idempotent_retry_same_order === outcome.idempotent_retry_attempts,
        created_orders: created.length,
        no_oversell: created.length <= successUsers,
      },
      drain_seconds: roundNumber(drainSeconds),
      still_queued: created.filter((flow) => flow.status === "queued").length,
      mq_metrics: {
        published_delta: metricValue(metricsAfter, "mq_messages_published_total") - metricValue(metricsBefore, "mq_messages_published_total"),
        consumed_success_delta: metricLabeled(metricsAfter, "mq_messages_consumed_total", "success") - metricLabeled(metricsBefore, "mq_messages_consumed_total", "success"),
      },
      resources: { containers: resources.containers, mysql: resources.mysql, redis: resources.redis, rabbitmq: resources.rabbitmq },
    };
    aggregate.results.push(runSummary);
    await writeFile(join(runDir, "load.json"), `${JSON.stringify(runSummary, null, 2)}\n`);
    await writeAggregate();
    console.error(`${label}: total_qps=${runSummary.total_qps} p99=${runSummary.execute.latency_ms.p99}ms primary=${outcome.primary_successes}/${successUsers} limit=${outcome.limit_repeat_rejected}/${outcome.limit_repeat_attempts} retry=${outcome.idempotent_retry_same_order}/${outcome.idempotent_retry_attempts} drain=${runSummary.drain_seconds}s`);
    await sleep(cooldownSeconds * 1000);
  }
}

await writeAggregate();
await writeFile(join(resultsDir, "mixed-staircase-summary.md"), renderMarkdown(aggregate));
console.log(JSON.stringify(aggregate, null, 2));

async function writeAggregate() {
  await writeFile(
    join(resultsDir, "mixed-staircase-summary.json"),
    `${JSON.stringify(aggregate, null, 2)}\n`,
  );
}

function buildFlows(availableUsers, limitRatio, retryRatio, primaryRatio, random) {
  const shuffled = [...availableUsers];
  for (let index = shuffled.length - 1; index > 0; index -= 1) {
    const target = Math.floor(random() * (index + 1));
    [shuffled[index], shuffled[target]] = [shuffled[target], shuffled[index]];
  }
  const limitCount = Math.floor(shuffled.length * limitRatio / primaryRatio);
  const retryCount = Math.floor(shuffled.length * retryRatio / primaryRatio);
  return shuffled.map((user, index) => ({
    ...user,
    followup: index < limitCount ? "limit_repeat" : index < limitCount + retryCount ? "idempotent_retry" : null,
    startDelayMs: Math.floor(random() * (startJitterMs + 1)),
    retryDelayMs: Math.floor(random() * (retryJitterMs + 1)),
    orderID: null,
    status: null,
  }));
}

async function executeRush(token, campaignID, key, metrics, label) {
  return httpJson("POST", `/rush-sales/${campaignID}/execute`, {
    token,
    label,
    metrics,
    okStatuses: [200, 400, 409, 429],
    headers: { "X-Idempotency-Key": key },
    body: { quantity: 1, contact_name: "混合压测用户", contact_phone: "13900139000", terms_accepted: true, attendees: [] },
  });
}

async function runWithConcurrency(items, concurrency, worker) {
  let next = 0;
  await Promise.all(Array.from({ length: concurrency }, async () => {
    while (true) {
      const index = next;
      next += 1;
      if (index >= items.length) return;
      await worker(items[index]);
    }
  }));
}

function newOutcome() {
  return { primary_attempts: 0, primary_successes: 0, primary_rejected: 0, limit_repeat_attempts: 0, limit_repeat_rejected: 0, limit_repeat_unexpected: 0, idempotent_retry_attempts: 0, idempotent_retry_same_order: 0, idempotent_retry_unexpected: 0 };
}

function countPrimary(outcome, status, orderID) {
  outcome.primary_attempts += 1;
  if (status === 200 && orderID) outcome.primary_successes += 1;
  else outcome.primary_rejected += 1;
}

function countFollowup(outcome, kind, status, originalOrderID, repeatOrderID) {
  if (kind === "limit_repeat") {
    outcome.limit_repeat_attempts += 1;
    if (status === 400 || status === 409 || status === 429) outcome.limit_repeat_rejected += 1;
    else outcome.limit_repeat_unexpected += 1;
    return;
  }
  outcome.idempotent_retry_attempts += 1;
  if (status === 200 && originalOrderID && originalOrderID === repeatOrderID) outcome.idempotent_retry_same_order += 1;
  else outcome.idempotent_retry_unexpected += 1;
}

function receiptOrderID(data) { return String(data?.data?.order_id || data?.data?.id || ""); }
function metricValue(text, name) { const match = text.match(new RegExp(`^${name}(?:\\{[^}]*\\})?\\s+([0-9.eE+-]+)$`, "m")); return match ? Number(match[1]) : 0; }
function metricLabeled(text, name, result) { const match = text.match(new RegExp(`^${name}\\{[^}]*result="${result}"[^}]*\\}\\s+([0-9.eE+-]+)$`, "m")); return match ? Number(match[1]) : 0; }
function parsePositiveInts(value) { return String(value).split(",").map(Number).filter((item) => Number.isInteger(item) && item > 0); }
function positiveInt(value, fallback) { const result = Number(value ?? fallback); if (!Number.isInteger(result) || result <= 0) throw new Error("expected positive integer"); return result; }
function nonNegativeInt(value, fallback) { const result = Number(value ?? fallback); if (!Number.isInteger(result) || result < 0) throw new Error("expected non-negative integer"); return result; }
function ratio(value, fallback) { const result = Number(value ?? fallback); if (!Number.isFinite(result) || result < 0 || result > 1) throw new Error("expected ratio in [0, 1]"); return result; }
function roundNumber(value) { return Number(Number(value).toFixed(2)); }
function median(values) { const sorted = [...values].sort((a, b) => a - b); return sorted[Math.floor(sorted.length / 2)] || 0; }
function mulberry32(seed) {
  let state = seed >>> 0;
  return () => {
    state += 0x6D2B79F5;
    let value = state;
    value = Math.imul(value ^ (value >>> 15), value | 1);
    value ^= value + Math.imul(value ^ (value >>> 7), value | 61);
    return ((value ^ (value >>> 14)) >>> 0) / 4294967296;
  };
}
function renderMarkdown(data) {
  const groups = new Map();
  for (const row of data.results) groups.set(row.concurrency, [...(groups.get(row.concurrency) || []), row]);
  const lines = ["# 抢票混合负载并发阶梯压测", "", `- 成功下单用户�?{data.workload.success_users}`, `- 混合比例：首次下�?50%，限购重�?${data.workload.limit_repeat_ratio * 100}%，幂等重�?${data.workload.idempotent_retry_ratio * 100}%`, `- 随机种子�?{data.workload.seed}`, "", "| 最大并�?| 轮次 | �?QPS 中位 | execute p99 中位 | MQ 追平中位 | 成功建单 | 限购拒绝 | 幂等同单 |", "| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |"];
  for (const [concurrency, rows] of [...groups.entries()].sort(([a], [b]) => a - b)) {
    lines.push(`| ${concurrency} | ${rows.length} | ${median(rows.map((row) => row.total_qps))} | ${median(rows.map((row) => row.execute.latency_ms.p99))}ms | ${median(rows.map((row) => row.drain_seconds))}s | ${median(rows.map((row) => row.outcome.primary_successes))}/${data.workload.success_users} | ${median(rows.map((row) => row.outcome.limit_repeat_rejected))}/${median(rows.map((row) => row.outcome.limit_repeat_attempts))} | ${median(rows.map((row) => row.outcome.idempotent_retry_same_order))}/${median(rows.map((row) => row.outcome.idempotent_retry_attempts))} |`);
  }
  return `${lines.join("\n")}\n`;
}
