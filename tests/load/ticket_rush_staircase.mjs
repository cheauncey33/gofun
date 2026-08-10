/**
 * 抢票成功建单阶梯压测：
 * - 一次准备最大用户池，避免把注册/登录计入每轮入口吞吐。
 * - 每档新建独立活动，按 STEPS × RUNS 执行。
 * - 只在 execute + MQ 追平期间采样容器、MySQL、Redis、RabbitMQ。
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
import { bootstrapRushCampaign } from "../integration/lib/catalog_bootstrap.mjs";

const steps = String(process.env.STEPS || "250,500,750,1000,1500")
  .split(",")
  .map(Number)
  .filter((value) => Number.isInteger(value) && value > 0);
const runs = Number(process.env.RUNS || 3);
const waveSize = Number(process.env.WAVE_SIZE || 50);
const pollSeconds = Number(process.env.POLL_SECONDS || 120);
const cooldownSeconds = Number(process.env.COOLDOWN_SECONDS || 5);
const setupConcurrency = Number(process.env.SETUP_CONCURRENCY || 20);
const runLabel = process.env.RUN_LABEL || `staircase-${Date.now()}`;
const resultsDir =
  process.env.RESULTS_DIR || join("tests", "load", "results", runLabel);
const metricsURL = process.env.METRICS_URL || "http://127.0.0.1:18080/metrics";

if (steps.length === 0) throw new Error("STEPS must contain positive integers");
if (!Number.isInteger(runs) || runs <= 0) throw new Error("RUNS must be positive");

await mkdir(resultsDir, { recursive: true });

const maxUsers = Math.max(...steps);
const setupMetrics = new Metrics("staircase_user_setup");
const users = [];
console.error(`preparing ${maxUsers} users with setup concurrency ${setupConcurrency}...`);
for (let offset = 0; offset < maxUsers; offset += setupConcurrency) {
  const indexes = Array.from(
    { length: Math.min(setupConcurrency, maxUsers - offset) },
    (_, idx) => offset + idx,
  );
  const batch = await Promise.all(
    indexes.map(async (index) => {
      const username = `${cfg.loadUserPrefix}stair_${index}`;
      const token = await ensureUser(username, cfg.loadPassword, setupMetrics);
      return { username, token };
    }),
  );
  users.push(...batch);
}

await sleep(cooldownSeconds * 1000);

const aggregate = {
  scenario: "ticket_rush_staircase",
  run_label: runLabel,
  created_at: new Date().toISOString(),
  config: {
    base_url: cfg.baseUrl,
    steps,
    runs,
    wave_size: waveSize,
    poll_seconds: pollSeconds,
    cooldown_seconds: cooldownSeconds,
    prepared_users: users.length,
    matrix_case: process.env.MATRIX_CASE || "",
    inventory_buckets_enabled: process.env.INVENTORY_BUCKETS_ENABLED || "",
    inventory_bucket_count: process.env.INVENTORY_BUCKET_COUNT || "",
    inventory_min_quota_to_bucket: process.env.INVENTORY_MIN_QUOTA_TO_BUCKET || "",
    inventory_bucket_retry: process.env.INVENTORY_BUCKET_RETRY || "",
    order_outbox: "transactional",
    order_consumer_worker_count: process.env.ORDER_CONSUMER_WORKER_COUNT || "",
    order_consumer_prefetch_count: process.env.ORDER_CONSUMER_PREFETCH_COUNT || "",
    order_outbox_publish_workers: process.env.ORDER_OUTBOX_PUBLISH_WORKERS || "",
    order_outbox_publish_batch: process.env.ORDER_OUTBOX_PUBLISH_BATCH || "",
    mysql_max_open_conns: process.env.MYSQL_MAX_OPEN_CONNS || "",
    mysql_max_idle_conns: process.env.MYSQL_MAX_IDLE_CONNS || "",
    redis_pool_size: process.env.REDIS_POOL_SIZE || "",
    rabbitmq_queue_type: process.env.RABBITMQ_QUEUE_TYPE || "",
    ratelimit_distributed_write_enabled: process.env.RATELIMIT_DISTRIBUTED_WRITE_ENABLED || "",
  },
  setup: setupMetrics.summary(),
  results: [],
};

for (const size of steps) {
  for (let round = 1; round <= runs; round += 1) {
    const label = `${runLabel}-${size}-r${round}`;
    const runDir = join(resultsDir, `${size}`, `r${round}`);
    await mkdir(runDir, { recursive: true });
    console.error(`starting ${label}...`);

    const campaign = await bootstrapRushCampaign({
      totalQuota: size + Math.max(100, Math.ceil(size * 0.1)),
      perUserLimit: 1,
      tierQuota: size + Math.max(100, Math.ceil(size * 0.1)),
      label,
    });
    const runUsers = users.slice(0, size).map((user) => ({
      ...user,
      orderID: null,
      status: null,
    }));
    const execute = new Metrics("staircase_execute");
    const poll = new Metrics("staircase_poll");
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
            `/rush-sales/${campaign.campaignID}/execute`,
            {
              token: user.token,
              label: "rush_execute",
              metrics: execute,
              okStatuses: [200, 400, 409, 429],
              headers: { "X-Idempotency-Key": randomUUID() },
              body: {
                quantity: 1,
                contact_name: "阶梯压测用户",
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

    const statusAfterPoll = {};
    for (const user of successes) {
      const status = user.status || "unknown";
      statusAfterPoll[status] = (statusAfterPoll[status] || 0) + 1;
    }
    const executeSummary = execute.summary();
    const runSummary = {
      label,
      size,
      round,
      campaign_id: campaign.campaignID,
      execute_seconds: roundNumber(executeSeconds),
      drain_seconds: roundNumber(drainSeconds),
      success_orders: successes.length,
      success_rate: roundNumber(successes.length / size),
      success_qps: roundNumber(successes.length / Math.max(executeSeconds, 0.001)),
      still_queued: successes.filter((user) => user.status === "queued").length,
      status_after_poll: statusAfterPoll,
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
    await writeFile(
      join(runDir, "load.json"),
      `${JSON.stringify(runSummary, null, 2)}\n`,
    );
    await writeAggregate();
    console.error(
      `${label}: success=${successes.length}/${size} qps=${runSummary.success_qps} ` +
        `p99=${executeSummary.latency_ms.p99}ms drain=${runSummary.drain_seconds}s`,
    );
    await sleep(cooldownSeconds * 1000);
  }
}

await writeAggregate();
console.log(JSON.stringify(aggregate, null, 2));

async function writeAggregate() {
  await writeFile(
    join(resultsDir, "staircase-summary.json"),
    `${JSON.stringify(aggregate, null, 2)}\n`,
  );
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
