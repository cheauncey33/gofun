import { randomUUID } from "node:crypto";
import { Metrics, cfg, ensureUser, httpJson, runWorkers, sleep } from "./lib/load_common.mjs";

const campaignID = process.env.RUSH_CAMPAIGN_ID;
if (!campaignID) {
  throw new Error("set RUSH_CAMPAIGN_ID to a live rush sale campaign id");
}

const metrics = new Metrics("ticket_rush_spike");

await runWorkers(metrics, async (workerID, deadline) => {
  const username = `${cfg.loadUserPrefix}rush_${workerID}`;
  let token;
  try {
    token = await ensureUser(username, cfg.loadPassword, metrics);
  } catch (err) {
    metrics.record("ensure_user", "error", 0, false, err.message);
    return;
  }

  while (performance.now() < deadline) {
    const tokenRes = await httpJson("POST", `/rush-sales/${campaignID}/token`, {
      token,
      label: "rush_token",
      metrics,
      okStatuses: [200, 400, 409, 429],
    });
    const rushToken = tokenRes.data?.data?.token;
    if (!rushToken) {
      if (cfg.thinkMs) await sleep(cfg.thinkMs);
      continue;
    }

    await httpJson("POST", `/rush-sales/${campaignID}/execute`, {
      token,
      label: "rush_execute",
      metrics,
      okStatuses: [200, 400, 409, 429],
      headers: { "X-Idempotency-Key": randomUUID() },
      body: {
        token: rushToken,
        quantity: 1,
        contact_name: "抢票用户",
        contact_phone: "13900139000",
        terms_accepted: true,
        attendees: [],
      },
    });

    if (cfg.thinkMs) await sleep(cfg.thinkMs);
  }
});
