import {
  Metrics,
  cfg,
  ensureUser,
  getProducts,
  httpJson,
  productID,
  randomInt,
  runWorkers,
  sleep,
} from "./lib/load_common.mjs";

const metrics = new Metrics("order_write");

await runWorkers(metrics, async (workerId, deadline) => {
  const username = `${cfg.loadUserPrefix}order_${workerId}`;
  const token = await ensureUser(username, cfg.loadPassword, metrics);
  let products = await getProducts(token, metrics, 50);

  while (performance.now() < deadline) {
    if (products.length === 0 || Math.random() < 0.05) {
      products = await getProducts(token, metrics, 50);
    }
    if (products.length > 0) {
      const product = products[randomInt(0, products.length - 1)];
      await httpJson("POST", "/orders", {
        token,
        label: "create_order",
        metrics,
        body: { items: [{ product_id: productID(product), num: 1 }] },
        okStatuses: [200, 400],
      });
    }
    if (cfg.thinkMs > 0) await sleep(cfg.thinkMs);
  }
});
