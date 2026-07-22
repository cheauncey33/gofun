import { randomUUID } from "node:crypto";
import { Metrics, cfg, ensureUser, httpJson, sleep } from "./lib/load_common.mjs";

const metrics = new Metrics("ticket_smoke");
const username = `${cfg.loadUserPrefix}ticket_smoke`;
const token = await ensureUser(username, cfg.loadPassword, metrics);

const eventsRes = await httpJson("GET", "/events?page=1&page_size=5", {
  token,
  label: "events_list",
  metrics,
});
const events = eventsRes.data?.data?.list || [];
if (!events.length) {
  throw new Error("ticket smoke failed: no published events");
}

const eventID = events[0].id;
const detailRes = await httpJson("GET", `/events/${eventID}`, {
  token,
  label: "event_detail",
  metrics,
});
const sessions = detailRes.data?.data?.sessions || [];
const tier = sessions.flatMap((s) => s.ticket_tiers || []).find((t) => t.status === "on_sale");
if (!tier) {
  throw new Error("ticket smoke failed: no on_sale ticket tier");
}

const createRes = await httpJson("POST", "/orders", {
  token,
  label: "create_ticket_order",
  metrics,
  headers: { "X-Idempotency-Key": randomUUID() },
  body: {
    ticket_tier_id: String(tier.id),
    quantity: 1,
    contact_name: "压测用户",
    contact_phone: "13800138000",
    terms_accepted: true,
    attendees: [],
  },
});
const orderID = createRes.data?.data?.order_id;
if (!orderID) {
  throw new Error(`ticket smoke failed: order not created: ${createRes.data?.msg || "unknown"}`);
}

let status = "queued";
for (let i = 0; i < 20 && status === "queued"; i += 1) {
  await sleep(500);
  const detail = await httpJson("GET", `/orders/${orderID}`, {
    token,
    label: "order_poll",
    metrics,
  });
  status = detail.data?.data?.status;
}

if (status === "pending_payment") {
  await httpJson("POST", `/orders/${orderID}/pay`, {
    token,
    label: "pay_order",
    metrics,
  });
}

await httpJson("GET", "/orders?page=1&page_size=5", {
  token,
  label: "orders_list",
  metrics,
});

console.log(JSON.stringify(metrics.summary({
  smoke_user: username,
  event_id: eventID,
  tier_id: tier.id,
  order_id: orderID,
  final_status: status,
}), null, 2));
