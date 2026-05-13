import {
  Metrics,
  cfg,
  getProducts,
  httpJson,
  login,
  pickWeighted,
  randomInt,
  runWorkers,
  sleep,
} from "./lib/load_common.mjs";

const metrics = new Metrics("read_baseline");
const token = await login(cfg.adminUsername, cfg.adminPassword, metrics);
const seedProducts = await getProducts(token, metrics, 50);

const endpoints = [
  { weight: 60, label: "products_list", path: () => `/products?page=${randomInt(1, 5)}&page_size=10` },
  {
    weight: 25,
    label: "product_detail",
    path: () => {
      const product = seedProducts[randomInt(0, Math.max(seedProducts.length - 1, 0))];
      return product ? `/products/${product.id}` : "/products?page=1&page_size=10";
    },
  },
  { weight: 10, label: "categories", path: () => "/categories" },
  { weight: 5, label: "seckill_list", path: () => "/seckill/activities?page=1&page_size=10" },
];

await runWorkers(metrics, async (_workerId, deadline) => {
  while (performance.now() < deadline) {
    const selected = pickWeighted(endpoints);
    await httpJson("GET", selected.path(), { token, label: selected.label, metrics });
    if (cfg.thinkMs > 0) await sleep(cfg.thinkMs);
  }
});
