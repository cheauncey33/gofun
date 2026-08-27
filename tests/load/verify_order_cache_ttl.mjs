/**
 * Verifies order list/detail cache bumps on terminal states (and create).
 * Expects a healthy backend at BASE_URL (default load-stack :18590).
 */
import { randomUUID } from "node:crypto";
import { assert, http, purchaseBody } from "../integration/lib/http.mjs";
import { bootstrapRushCampaign } from "../integration/lib/catalog_bootstrap.mjs";

const baseHint = process.env.BASE_URL || "http://127.0.0.1:18590/api/v1";
process.env.BASE_URL = baseHint;

const timeoutMs = Number(process.env.E2E_TIMEOUT_MS || 45000);

async function waitFor(label, read, predicate, waitMs = timeoutMs) {
  const deadline = Date.now() + waitMs;
  let lastValue;
  while (Date.now() < deadline) {
    lastValue = await read();
    if (predicate(lastValue)) return lastValue;
    await new Promise((r) => setTimeout(r, 120));
  }
  throw new Error(`${label} timeout: ${JSON.stringify(lastValue)}`);
}

async function listOrders(token) {
  const response = await http("GET", "/orders?page=1&page_size=20", { token });
  assert(response.ok, `list orders failed: ${JSON.stringify(response.data)}`);
  const data = response.data.data;
  if (Array.isArray(data)) return data;
  return data?.list || data?.orders || [];
}

function findStatus(orders, orderID) {
  const id = String(orderID);
  const row = (orders || []).find((o) => String(o.id) === id);
  return row?.status || null;
}

const fixture = await bootstrapRushCampaign({
  totalQuota: 20,
  tierQuota: 40,
  perUserLimit: 5,
  maxTicketsPerOrder: 2,
  label: `order-cache-ttl-${Date.now()}`,
});
const token = fixture.owner.token;

const createResp = await http("POST", "/orders", {
  token,
  headers: { "X-Idempotency-Key": randomUUID() },
  body: {
    ticket_tier_id: String(fixture.tierID),
    ...purchaseBody("缓存TTL校验"),
    quantity: 1,
  },
});
assert(createResp.ok && createResp.data?.data?.order_id, `create failed: ${JSON.stringify(createResp.data)}`);
const orderID = String(createResp.data.data.order_id);

await waitFor("pending_payment", () => http("GET", `/orders/${orderID}`, { token }).then((r) => r.data?.data), (o) =>
  o && o.status === "pending_payment",
);

const afterCreate = await listOrders(token);
const statusAfterCreate = findStatus(afterCreate, orderID);
assert(statusAfterCreate === "pending_payment", `create bump missing: list status=${statusAfterCreate}`);

const cancelResp = await http("POST", `/orders/${orderID}/cancel`, {
  token,
  body: { reason: "order-cache-ttl-verify" },
});
assert(cancelResp.ok, `cancel failed: ${JSON.stringify(cancelResp.data)}`);

const t0 = Date.now();
const afterCancel = await listOrders(token);
const statusAfterCancel = findStatus(afterCancel, orderID);
const cancelLatencyMs = Date.now() - t0;
assert(
  statusAfterCancel === "cancelled",
  `terminal bump missing: expected cancelled immediately, got ${statusAfterCancel} (list took ${cancelLatencyMs}ms)`,
);

// Non-terminal: queued→pending is already past; create another and confirm list stays
// coherent after pay (terminal paid bump).
const create2 = await http("POST", "/orders", {
  token,
  headers: { "X-Idempotency-Key": randomUUID() },
  body: {
    ticket_tier_id: String(fixture.tierID),
    ...purchaseBody("缓存TTL支付"),
    quantity: 1,
  },
});
assert(create2.ok && create2.data?.data?.order_id, `create2 failed: ${JSON.stringify(create2.data)}`);
const order2 = String(create2.data.data.order_id);
await waitFor("pending_payment#2", () => http("GET", `/orders/${order2}`, { token }).then((r) => r.data?.data), (o) =>
  o && o.status === "pending_payment",
);

const pay = await http("POST", `/orders/${order2}/pay`, {
  token,
  body: { scenario: "success" },
});
assert(pay.ok, `pay failed: ${JSON.stringify(pay.data)}`);
await waitFor("paid", () => http("GET", `/orders/${order2}`, { token }).then((r) => r.data?.data), (o) =>
  o && o.status === "paid",
);

const t1 = Date.now();
const afterPaid = await listOrders(token);
const statusAfterPaid = findStatus(afterPaid, order2);
const paidLatencyMs = Date.now() - t1;
assert(
  statusAfterPaid === "paid",
  `paid bump missing: expected paid immediately, got ${statusAfterPaid} (list took ${paidLatencyMs}ms)`,
);

console.log(
  JSON.stringify(
    {
      ok: true,
      order_cancelled: orderID,
      cancel_list_status: statusAfterCancel,
      cancel_list_ms: cancelLatencyMs,
      order_paid: order2,
      paid_list_status: statusAfterPaid,
      paid_list_ms: paidLatencyMs,
      note: "terminal states bump ver immediately; short TTL only covers non-terminal churn",
    },
    null,
    2,
  ),
);
