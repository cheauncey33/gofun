/**
 * 抢票幂等回归：同幂等键连续/并发 Execute，
 * 应只产生一笔订单，且不应永久多扣 Redis 票额。
 *
 * 用法（gofun-it 已启动）:
 *   $env:BASE_URL='http://127.0.0.1:18080/api/v1'
 *   $env:RUSH_CAMPAIGN_ID='<id>'
 *   node tests/integration/rush_idempotency.mjs
 */
import { randomUUID } from "node:crypto";

const baseUrl = process.env.BASE_URL || "http://127.0.0.1:18080/api/v1";
const campaignID = process.env.RUSH_CAMPAIGN_ID;
const username = process.env.LOAD_USERNAME || `rushidem${String(Date.now()).slice(-8)}`;
const password = process.env.LOAD_PASSWORD || "12345678";

if (!campaignID) {
  console.error("RUSH_CAMPAIGN_ID is required");
  process.exit(1);
}

async function http(method, path, { token, body, headers = {} } = {}) {
  const res = await fetch(`${baseUrl}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: token } : {}),
      ...headers,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  return { status: res.status, data };
}

async function ensureUser() {
  await http("POST", "/register", {
    body: { username, password, dorm_id: 1 },
  });
  const login = await http("POST", "/login", { body: { username, password } });
  const token = login.data?.data?.access_token || login.data?.data?.token;
  if (!token) {
    throw new Error(`login failed: ${JSON.stringify(login.data)}`);
  }
  // 本项目 Authorization 头直接传 JWT，不加 Bearer 前缀。
  return token;
}

async function execute(token, idempotencyKey) {
  return http("POST", `/rush-sales/${campaignID}/execute`, {
    token,
    headers: { "X-Idempotency-Key": idempotencyKey },
    body: {
      quantity: 1,
      contact_name: "幂等测试",
      contact_phone: "13800138000",
      terms_accepted: true,
      attendees: [],
    },
  });
}

const auth = await ensureUser();
const idempotencyKey = randomUUID();

const first = await execute(auth, idempotencyKey);
if (first.status !== 200 || !first.data?.data?.order_id) {
  throw new Error(`first execute failed: ${JSON.stringify(first)}`);
}
const orderID = String(first.data.data.order_id);

const [second, third] = await Promise.all([
  execute(auth, idempotencyKey),
  execute(auth, idempotencyKey),
]);

const ids = [second, third]
  .filter((item) => item.status === 200 && item.data?.data?.order_id)
  .map((item) => String(item.data.data.order_id));

for (const id of ids) {
  if (id !== orderID) {
    throw new Error(`idempotency broken: got extra order ${id}, want ${orderID}`);
  }
}

const list = await http("GET", "/orders?page=1&page_size=50", { token: auth });
const sameKeyOrders = (list.data?.data?.list || []).filter(
  (order) => String(order.id) === orderID,
);
if (sameKeyOrders.length !== 1) {
  throw new Error(`expected one order row for ${orderID}, got ${sameKeyOrders.length}`);
}

console.log(JSON.stringify({
  ok: true,
  username,
  campaign_id: campaignID,
  order_id: orderID,
  retry_status: [second.status, third.status],
}, null, 2));
