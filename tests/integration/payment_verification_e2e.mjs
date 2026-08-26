import { randomUUID } from "node:crypto";
import { assert, http, purchaseBody } from "./lib/http.mjs";
import { bootstrapRushCampaign } from "./lib/catalog_bootstrap.mjs";

const timeoutMs = Number(process.env.E2E_TIMEOUT_MS || 30000);
const paymentTimeoutWaitMs = Number(process.env.E2E_PAYMENT_TIMEOUT_MS || 80000);

async function waitFor(label, read, predicate, waitMs = timeoutMs) {
  const deadline = Date.now() + waitMs;
  let lastValue;
  while (Date.now() < deadline) {
    lastValue = await read();
    if (predicate(lastValue)) return lastValue;
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`${label} timeout: ${JSON.stringify(lastValue)}`);
}

async function createOrder(fixture, contactName, quantity = 1) {
  const response = await http("POST", "/orders", {
    token: fixture.owner.token,
    headers: { "X-Idempotency-Key": randomUUID() },
    body: {
      ticket_tier_id: String(fixture.tierID),
      ...purchaseBody(contactName),
      quantity,
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
  totalQuota: 12,
  tierQuota: 30,
  perUserLimit: 20,
  maxTicketsPerOrder: 5,
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

const retryPayResponse = await http("POST", `/orders/${successOrderID}/pay`, {
  token: fixture.owner.token,
  body: { scenario: "success" },
});
assert(retryPayResponse.ok &&
  retryPayResponse.data?.data?.payment?.payment_no === payResponse.data.data.payment.payment_no,
  `payment retry should reuse and reschedule the pending payment: ${JSON.stringify(retryPayResponse.data)}`);

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

const timeoutOrderID = await createOrder(fixture, "支付超时");
await waitFor("timeout order pending_payment", () => getOrder(fixture.owner.token, timeoutOrderID), (order) =>
  order.status === "pending_payment",
);
const timeoutPay = await http("POST", `/orders/${timeoutOrderID}/pay`, {
  token: fixture.owner.token,
  body: { scenario: "timeout" },
});
assert(timeoutPay.ok, `start timeout sandbox payment failed: ${JSON.stringify(timeoutPay.data)}`);
const timeoutOrder = await waitFor("payment timeout cancellation", () =>
  getOrder(fixture.owner.token, timeoutOrderID),
  (order) => order.status === "cancelled" && order.payment_status !== "paid",
  paymentTimeoutWaitMs,
);

const refundOrderID = await createOrder(fixture, "支付退款");
await waitFor("refund order pending_payment", () => getOrder(fixture.owner.token, refundOrderID), (order) =>
  order.status === "pending_payment",
);
await http("POST", `/orders/${refundOrderID}/pay`, {
  token: fixture.owner.token,
  body: { scenario: "success" },
});
const refundPaidOrder = await waitFor("refund order paid", () =>
  getOrder(fixture.owner.token, refundOrderID),
  (order) => order.status === "paid" && order.payment_status === "paid" &&
    Array.isArray(order.tickets) && order.tickets.length === 1 && order.tickets[0].credential,
);
const refundResponse = await http("POST", `/orders/${refundOrderID}/cancel`, {
  token: fixture.owner.token,
  body: { reason: "E2E refund" },
});
assert(refundResponse.ok, `refund order cancellation failed: ${JSON.stringify(refundResponse.data)}`);
const refundedOrder = await waitFor("refund completed", () =>
  getOrder(fixture.owner.token, refundOrderID),
  (order) => order.status === "cancelled" && order.payment_status === "refunded" &&
    order.tickets?.every((ticket) => ticket.status === "revoked"),
);

const multiOrderID = await createOrder(fixture, "多票订单", 2);
await waitFor("multi-ticket order pending_payment", () => getOrder(fixture.owner.token, multiOrderID), (order) =>
  order.status === "pending_payment",
);
await http("POST", `/orders/${multiOrderID}/pay`, {
  token: fixture.owner.token,
  body: { scenario: "success" },
});
const multiOrder = await waitFor("multi-ticket order paid", () =>
  getOrder(fixture.owner.token, multiOrderID),
  (order) => order.status === "paid" && order.tickets?.length === 2 &&
    order.tickets.every((ticket) => ticket.credential),
);
const concurrentCredential = multiOrder.tickets[0].credential;
const [concurrentVerifyA, concurrentVerifyB] = await Promise.all([
  http("POST", `/organizers/${fixture.organizerID}/verifications`, {
    token: fixture.owner.token,
    body: { credential: concurrentCredential, session_id: String(fixture.sessionID) },
  }),
  http("POST", `/organizers/${fixture.organizerID}/verifications`, {
    token: fixture.owner.token,
    body: { credential: concurrentCredential, session_id: String(fixture.sessionID) },
  }),
]);
const concurrentResults = [concurrentVerifyA.data?.data?.result, concurrentVerifyB.data?.data?.result];
assert(concurrentResults.includes("success") && concurrentResults.includes("already_used"),
  `concurrent verification should be single-use: ${JSON.stringify([concurrentVerifyA.data, concurrentVerifyB.data])}`);

const refundVerifyRaceOrderID = await createOrder(fixture, "退款核验并发");
await waitFor("refund and verification order pending_payment", () =>
  getOrder(fixture.owner.token, refundVerifyRaceOrderID),
  (order) => order.status === "pending_payment",
);
await http("POST", `/orders/${refundVerifyRaceOrderID}/pay`, {
  token: fixture.owner.token,
  body: { scenario: "success" },
});
const refundVerifyRacePaid = await waitFor("refund and verification order paid", () =>
  getOrder(fixture.owner.token, refundVerifyRaceOrderID),
  (order) => order.status === "paid" && order.payment_status === "paid" &&
    order.tickets?.[0]?.credential,
);
const [raceCancel, raceVerify] = await Promise.all([
  http("POST", `/orders/${refundVerifyRaceOrderID}/cancel`, {
    token: fixture.owner.token,
    body: { reason: "E2E refund verification race" },
  }),
  http("POST", `/organizers/${fixture.organizerID}/verifications`, {
    token: fixture.owner.token,
    body: {
      credential: refundVerifyRacePaid.tickets[0].credential,
      session_id: String(fixture.sessionID),
    },
  }),
]);
const raceOrder = await waitFor("refund and verification race settled", () =>
  getOrder(fixture.owner.token, refundVerifyRaceOrderID),
  (order) => order.status === "cancelled" || order.status === "paid",
);
if (raceCancel.ok) {
  assert(raceOrder.status === "cancelled" && raceOrder.payment_status === "refunded" &&
    raceVerify.ok && raceVerify.data?.data?.result === "revoked",
  `refund-first race should revoke verification: ${JSON.stringify({ raceCancel: raceCancel.data, raceVerify: raceVerify.data, raceOrder })}`);
} else {
  assert(raceCancel.status === 400 && raceOrder.status === "paid" && raceOrder.payment_status === "paid" &&
    raceVerify.ok && raceVerify.data?.data?.result === "success",
  `verification-first race should preserve paid order: ${JSON.stringify({ raceCancel: raceCancel.data, raceVerify: raceVerify.data, raceOrder })}`);
}

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
  timeout_order_id: timeoutOrderID,
  timeout_status: timeoutOrder.status,
  refund_order_id: refundOrderID,
  refund_status: refundedOrder.payment_status,
  multi_ticket_count: multiOrder.tickets.length,
  concurrent_verification: concurrentResults,
  refund_verification_race: {
    cancel_ok: raceCancel.ok,
    verification_result: raceVerify.data?.data?.result,
    final_order_status: raceOrder.status,
  },
  ticket_id: String(paidOrder.tickets[0].id),
  verification: verifyResponse.data.data.result,
  duplicate_verification: duplicateVerify.data.data.result,
  failed_payment_status: failedOrder.payment_status,
}, null, 2));
