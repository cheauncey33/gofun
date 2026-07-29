/**
 * 成功建单路径压测：大用户池每人抢一次，统计 200 成功吞吐，并轮询订单进入 pending_payment。
 *
 * 环境变量：
 *   RUSH_CAMPAIGN_ID, BASE_URL, SUCCESS_USERS (默认 200), WAVE_SIZE (默认 50)
 */
import { randomUUID } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { Metrics, cfg, ensureUser, httpJson, sleep } from "./lib/load_common.mjs";

const campaignID = process.env.RUSH_CAMPAIGN_ID;
if (!campaignID) throw new Error("set RUSH_CAMPAIGN_ID");

const userCount = Number(process.env.SUCCESS_USERS || 200);
const waveSize = Number(process.env.WAVE_SIZE || 50);
const pollSeconds = Number(process.env.POLL_SECONDS || 30);
const runLabel = process.env.RUN_LABEL || `success-path-${Date.now()}`;
const outDir = process.env.RESULTS_DIR || join("tests", "load", "results", runLabel);
const metricsURL = process.env.METRICS_URL || "http://127.0.0.1:18080/metrics";

await mkdir(outDir, { recursive: true });

const setup = new Metrics("success_path_setup");
const execute = new Metrics("success_path_execute");
const poll = new Metrics("success_path_poll");

console.error(`preparing ${userCount} users...`);
const users = [];
for (let i = 0; i < userCount; i += 1) {
  const username = `${cfg.loadUserPrefix}ok_${i}`;
  try {
    const token = await ensureUser(username, cfg.loadPassword, setup);
    users.push({ username, token, orderID: null, status: null });
  } catch (err) {
    setup.record("ensure_user", "error", 0, false, err.message);
  }
}
if (users.length === 0) throw new Error("no users prepared");

const metricsBefore = await fetch(metricsURL).then((r) => r.text());
await writeFile(join(outDir, "metrics-before.prom"), metricsBefore);

const started = performance.now();
for (let offset = 0; offset < users.length; offset += waveSize) {
  const wave = users.slice(offset, offset + waveSize);
  await Promise.all(wave.map(async (user) => {
    const { data, ok, res } = await httpJson("POST", `/rush-sales/${campaignID}/execute`, {
      token: user.token,
      label: "rush_execute",
      metrics: execute,
      okStatuses: [200, 400, 409, 429],
      headers: { "X-Idempotency-Key": randomUUID() },
      body: {
        quantity: 1,
        contact_name: "成功路径用户",
        contact_phone: "13900139000",
        terms_accepted: true,
        attendees: [],
      },
    });
    if (ok && res?.status === 200) {
      user.orderID = String(data?.data?.order_id || data?.data?.id || "");
      user.status = data?.data?.status || "queued";
    }
  }));
}
const executeElapsed = (performance.now() - started) / 1000;

const successes = users.filter((u) => u.orderID);
const pollStarted = performance.now();
const deadline = pollStarted + pollSeconds * 1000;
while (performance.now() < deadline) {
  // queued -> pending_payment after MQ consume
  const stillQueued = successes.filter((u) => !u.status || u.status === "queued");
  if (stillQueued.length === 0) break;
  await Promise.all(stillQueued.slice(0, waveSize).map(async (user) => {
    const { data, ok } = await httpJson("GET", `/orders/${user.orderID}`, {
      token: user.token,
      label: "order_poll",
      metrics: poll,
      okStatuses: [200],
    });
    if (ok) user.status = data?.data?.status || user.status;
  }));
  await sleep(200);
}
const drainSeconds = Number(((performance.now() - pollStarted) / 1000).toFixed(2));

const metricsAfter = await fetch(metricsURL).then((r) => r.text());
await writeFile(join(outDir, "metrics-after.prom"), metricsAfter);

const byStatus = {};
for (const u of successes) {
  byStatus[u.status || "unknown"] = (byStatus[u.status || "unknown"] || 0) + 1;
}

const summary = {
  scenario: "ticket_rush_success_path",
  run_label: runLabel,
  created_at: new Date().toISOString(),
  campaign_id: campaignID,
  prepared_users: users.length,
  execute_seconds: Number(executeElapsed.toFixed(2)),
  drain_seconds: drainSeconds,
  consume_qps: Number((successes.length / Math.max(drainSeconds, 0.001)).toFixed(2)),
  worker_hint: process.env.ORDER_CONSUMER_WORKER_COUNT || "",
  success_orders: successes.length,
  success_qps: Number((successes.length / Math.max(executeElapsed, 0.001)).toFixed(2)),
  status_after_poll: byStatus,
  pending_or_paid: successes.filter((u) =>
    ["pending_payment", "paid"].includes(u.status),
  ).length,
  still_queued: successes.filter((u) => u.status === "queued").length,
  execute: execute.summary(),
  setup: setup.summary(),
  poll: poll.summary(),
  mq_metrics: {
    published_before: metricValue(metricsBefore, "mq_messages_published_total"),
    published_after: metricValue(metricsAfter, "mq_messages_published_total"),
    consumed_success_before: metricLabeled(metricsBefore, "mq_messages_consumed_total", "success"),
    consumed_success_after: metricLabeled(metricsAfter, "mq_messages_consumed_total", "success"),
  },
};

summary.mq_metrics.published_delta =
  (summary.mq_metrics.published_after ?? 0) - (summary.mq_metrics.published_before ?? 0);
summary.mq_metrics.consumed_success_delta =
  (summary.mq_metrics.consumed_success_after ?? 0) -
  (summary.mq_metrics.consumed_success_before ?? 0);

await writeFile(join(outDir, "load.json"), `${JSON.stringify(summary, null, 2)}\n`);
console.log(JSON.stringify(summary, null, 2));

function metricValue(text, name) {
  const re = new RegExp(`^${name}(?:\\{[^}]*\\})?\\s+([0-9.eE+-]+)$`, "m");
  const m = text.match(re);
  return m ? Number(m[1]) : null;
}

function metricLabeled(text, name, result) {
  const re = new RegExp(
    `^${name}\\{[^}]*result="${result}"[^}]*\\}\\s+([0-9.eE+-]+)$`,
    "m",
  );
  const m = text.match(re);
  return m ? Number(m[1]) : null;
}
