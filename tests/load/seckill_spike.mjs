import {
  Metrics,
  cfg,
  ensureUser,
  httpJson,
  login,
  runWorkers,
  sleep,
} from "./lib/load_common.mjs";

const metrics = new Metrics("seckill_spike");
const adminToken = await login(cfg.adminUsername, cfg.adminPassword, metrics);
const list = await httpJson("GET", "/seckill/activities?page=1&page_size=20", {
  token: adminToken,
  label: "seckill_list",
  metrics,
});
const activity = list.data?.data?.list?.find((item) => item.status === 1);
if (!activity) {
  throw new Error("no active seckill activity found; create and warm up one before running seckill_spike");
}

await runWorkers(metrics, async (workerId, deadline) => {
  const username = `sec_${workerId}`;
  const token = await ensureUser(username, cfg.loadPassword, metrics);

  while (performance.now() < deadline) {
    const tokenRes = await httpJson("POST", `/seckill/activities/${activity.id}/token`, {
      token,
      label: "seckill_token",
      metrics,
      body: {},
      okStatuses: [200, 400],
    });
    const seckillToken = tokenRes.data?.data?.token;
    if (seckillToken) {
      await httpJson("POST", `/seckill/activities/${activity.id}/execute`, {
        token,
        label: "seckill_execute",
        metrics,
        body: { token: seckillToken, quantity: 1 },
        okStatuses: [200, 400],
      });
    }
    if (cfg.thinkMs > 0) await sleep(cfg.thinkMs);
  }
});
