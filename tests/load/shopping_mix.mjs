import {
  Metrics,
  cfg,
  ensureUser,
  getProducts,
  httpJson,
  pickWeighted,
  productID,
  randomInt,
  runWorkers,
  sleep,
} from "./lib/load_common.mjs";

const metrics = new Metrics("shopping_mix");

async function browse(token) {
  const products = await getProducts(token, metrics, 10);
  if (products.length > 0) {
    const product = products[randomInt(0, products.length - 1)];
    await httpJson("GET", `/products/${product.id}`, { token, label: "product_detail", metrics });
  }
  return products;
}

await runWorkers(metrics, async (workerId, deadline) => {
  const username = `${cfg.loadUserPrefix}${workerId}`;
  const token = await ensureUser(username, cfg.loadPassword, metrics);

  while (performance.now() < deadline) {
    const action = pickWeighted([
      { weight: 45, name: "browse" },
      { weight: 20, name: "detail" },
      { weight: 15, name: "create_order" },
      { weight: 10, name: "orders_list" },
      { weight: 5, name: "user_info" },
      { weight: 5, name: "seckill_list" },
    ]).name;

    if (action === "browse" || action === "detail") {
      await browse(token);
    } else if (action === "create_order") {
      const products = await getProducts(token, metrics, 20);
      if (products.length > 0) {
        const product = products[randomInt(0, products.length - 1)];
        await httpJson("POST", "/orders", {
          token,
          label: "create_order",
          metrics,
          body: { items: [{ product_id: productID(product), num: 1 }] },
        });
      }
    } else if (action === "orders_list") {
      await httpJson("GET", "/orders?page=1&page_size=10", { token, label: "orders_list", metrics });
    } else if (action === "user_info") {
      await httpJson("GET", "/user/info", { token, label: "user_info", metrics });
    } else {
      await httpJson("GET", "/seckill/activities?page=1&page_size=10", { token, label: "seckill_list", metrics });
    }

    if (cfg.thinkMs > 0) await sleep(cfg.thinkMs);
  }
});
