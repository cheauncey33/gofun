import { randomUUID } from "node:crypto";
import { assert, http, purchaseBody } from "./lib/http.mjs";
import { bootstrapRushCampaign } from "./lib/catalog_bootstrap.mjs";

const timeoutMs = Number(process.env.E2E_TIMEOUT_MS || 30000);

async function waitFor(label, read, predicate) {
  const deadline = Date.now() + timeoutMs;
  let lastValue;
  while (Date.now() < deadline) {
    lastValue = await read();
    if (predicate(lastValue)) return lastValue;
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`${label} timeout: ${JSON.stringify(lastValue)}`);
}

async function createOrder(fixture, contactName) {
  const response = await http("POST", "/orders", {
    token: fixture.owner.token,
    headers: { "X-Idempotency-Key": randomUUID() },
    body: {
      ticket_tier_id: String(fixture.tierID),
      ...purchaseBody(contactName),
    },
  });
  assert(response.ok && response.data?.data?.order_id, `create order failed: ${JSON.stringify(response.data)}`);
  return String(response.data.data.order_id);
}

async function getOrder(token, orderID) {
  const response = await http("GET", `/orders/${orderID}`, { token });
  assert(response.ok, `get order ${orderID} failed: ${JSON.stringify(response.data)}`);
  return response.data.data;
}

const fixture = await bootstrapRushCampaign({
  totalQuota: 4,
  tierQuota: 10,
  perUserLimit: 4,
  label: "it-payment-verification",
});

const successOrderID = await createOrder(fixture, "支付核销成功");
await waitFor("order pending_payment", () => getOrder(fixture.owner.token, successOrderID), (order) =>
  order.status === "pending_payment",
);

const payResponse = await http("POST", `/orders/${successOrderID}/pay`, {
  token: fixture.owner.token,
  body: { scenario: "success" },
});
assert(payResponse.ok && payResponse.data?.data?.payment?.status === "pending",
  `start sandbox payment failed: ${JSON.stringify(payResponse.data)}`);

const paidOrder = await waitFor("payment success and ticket issue", () =>
  getOrder(fixture.owner.token, successOrderID),
  (order) => order.status === "paid" && order.payment_status === "paid" &&
    Array.isArray(order.tickets) && order.tickets.length === 1 && order.tickets[0].credential,
);
const credential = paidOrder.tickets[0].credential;

const verifyResponse = await http("POST", `/organizers/${fixture.organizerID}/verifications`, {
  token: fixture.owner.token,
  body: { credential, session_id: String(fixture.sessionID) },
});
assert(verifyResponse.ok && verifyResponse.data?.data?.result === "success",
  `ticket verification failed: ${JSON.stringify(verifyResponse.data)}`);

const duplicateVerify = await http("POST", `/organizers/${fixture.organizerID}/verifications`, {
  token: fixture.owner.token,
  body: { credential, session_id: String(fixture.sessionID) },
});
assert(duplicateVerify.ok && duplicateVerify.data?.data?.result === "already_used",
  `duplicate verification should be rejected: ${JSON.stringify(duplicateVerify.data)}`);

const failedOrderID = await createOrder(fixture, "支付失败");
await waitFor("failed order pending_payment", () => getOrder(fixture.owner.token, failedOrderID), (order) =>
  order.status === "pending_payment",
);
const failedPay = await http("POST", `/orders/${failedOrderID}/pay`, {
  token: fixture.owner.token,
  body: { scenario: "failed" },
});
assert(failedPay.ok, `start failed sandbox payment failed: ${JSON.stringify(failedPay.data)}`);
const failedOrder = await waitFor("payment failure", () => getOrder(fixture.owner.token, failedOrderID), (order) =>
  order.payment_status === "failed" && (!order.tickets || order.tickets.length === 0),
);

const invalidCallback = await http("POST", "/payments/sandbox/callback", {
  body: {
    provider_event_id: `invalid_${randomUUID()}`,
    payment_no: "unknown",
    provider: "sandbox",
    status: "success",
    amount_cents: 1,
    signature: "invalid",
  },
});
assert(invalidCallback.status === 400, `invalid callback should be rejected: ${JSON.stringify(invalidCallback.data)}`);

console.log(JSON.stringify({
  ok: true,
  success_order_id: successOrderID,
  failed_order_id: failedOrderID,
  ticket_id: String(paidOrder.tickets[0].id),
  verification: verifyResponse.data.data.result,
  duplicate_verification: duplicateVerify.data.data.result,
  failed_payment_status: failedOrder.payment_status,
}, null, 2));
