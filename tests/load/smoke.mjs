import { Metrics, cfg, ensureUser, getProducts, httpJson, productID } from "./lib/load_common.mjs";

const metrics = new Metrics("smoke");
const username = `${cfg.loadUserPrefix}smoke`;
const token = await ensureUser(username, cfg.loadPassword, metrics);

const products = await getProducts(token, metrics, 5);
if (products.length === 0) {
  throw new Error("smoke failed: no products returned");
}

await httpJson("GET", `/products/${products[0].id}`, {
  token,
  label: "product_detail",
  metrics,
});

await httpJson("GET", "/categories", {
  token,
  label: "categories",
  metrics,
});

await httpJson("GET", "/user/info", {
  token,
  label: "user_info",
  metrics,
});

await httpJson("POST", "/orders", {
  token,
  label: "create_order",
  metrics,
  body: { items: [{ product_id: productID(products[0]), num: 1 }] },
});

await httpJson("GET", "/orders?page=1&page_size=5", {
  token,
  label: "orders_list",
  metrics,
});

console.log(JSON.stringify(metrics.summary({ smoke_user: username }), null, 2));
