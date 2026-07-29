import { randomUUID } from "node:crypto";
import { Metrics, cfg, ensureUser, httpJson, runWorkers, sleep } from "./lib/load_common.mjs";

const campaignID = process.env.RUSH_CAMPAIGN_ID;
if (!campaignID) {
  throw new Error("set RUSH_CAMPAIGN_ID to a live rush sale campaign id");
}

const setupMetrics = new Metrics("ticket_rush_setup");
const tokens = await Promise.all(Array.from({ length: cfg.concurrency }, async (_, workerID) => {
  const username = `${cfg.loadUserPrefix}rush_${workerID}`;
  try {
    return await ensureUser(username, cfg.loadPassword, setupMetrics);
  } catch (err) {
    setupMetrics.record("ensure_user", "error", 0, false, err.message);
    return null;
  }
}));
if (!tokens.some(Boolean)) {
  throw new Error("no load user could be prepared; check username length, credentials, and rate limits");
}
const metrics = new Metrics("ticket_rush_spike");

await runWorkers(metrics, async (workerID, deadline) => {
  const token = tokens[workerID];
  if (!token) return;

  while (performance.now() < deadline) {
    await httpJson("POST", `/rush-sales/${campaignID}/execute`, {
      token,
      label: "rush_execute",
      metrics,
      okStatuses: [200, 400, 409, 429],
      headers: { "X-Idempotency-Key": randomUUID() },
      body: {
        quantity: 1,
        contact_name: "抢票用户",
        contact_phone: "13900139000",
        terms_accepted: true,
        attendees: [],
      },
    });

    if (cfg.thinkMs) await sleep(cfg.thinkMs);
  }
}, { setup: setupMetrics.summary() });
