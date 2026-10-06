import http from "k6/http";
import exec from "k6/execution";
import { check } from "k6";
import { SharedArray } from "k6/data";
import { Counter, Trend } from "k6/metrics";
import { classifyResponse } from "./business_result.mjs";

const metadata = new SharedArray("scenario_metadata", () => {
  const { users, ...data } = JSON.parse(open(__ENV.K6_FIXTURE));
  return [data];
})[0];
const users = new SharedArray("scenario_users", () => JSON.parse(open(__ENV.K6_FIXTURE)).users);
const mode = __ENV.MODE;
const rate = Number(__ENV.RATE || 0);
const iterations = Number(__ENV.ITERATIONS || 1000);
const duration = __ENV.DURATION || "10s";
const groups = Number(__ENV.GROUPS || 100);
const base = __ENV.BASE_URL;
const outcomes = ["accepted", "sold_out", "business_rejected", "rate_limited", "server_error", "transport_error", "unexpected"];
const counters = Object.fromEntries(outcomes.map((name) => [name, new Counter(`scenario_${name}`)]));
const acceptedLatency = new Trend("accepted_latency", true);
const rejectedLatency = new Trend("rejected_latency", true);
const latency = new Trend("scenario_latency", true);
http.setResponseCallback(http.expectedStatuses(200, 400, 409, 429));

export const options = {
  summaryTrendStats: ["avg", "p(95)", "p(99)", "max"],
  thresholds: { checks: ["rate==1"], scenario_latency: ["p(99)<1000"] },
  scenarios: {
    business: rate > 0
      ? { executor: "constant-arrival-rate", rate, timeUnit: "1s", duration, preAllocatedVUs: 100, maxVUs: 300 }
      : { executor: "shared-iterations", vus: 100, iterations, maxDuration: "2m" },
  },
};

export default function () {
  const index = exec.scenario.iterationInTest;
  const singleUser = mode === "rate_limit" || mode === "purchase_limit";
  const userIndex = singleUser ? 0 : mode === "duplicate" ? index % groups : index % users.length;
  const user = users[userIndex];
  const key = mode === "duplicate" ? `duplicate-${metadata.campaign_id}-${userIndex}`
    : mode === "rate_limit" ? `rate-limit-${metadata.campaign_id}` : `scenario-${metadata.campaign_id}-${index}`;
  const response = http.post(`${base}/rush-sales/${metadata.campaign_id}/execute`, JSON.stringify({
    quantity: 1, contact_name: "场景压测", contact_phone: "13900139000", terms_accepted: true, attendees: [],
  }), { headers: { Authorization: user.token, "Content-Type": "application/json", "X-Idempotency-Key": key }, tags: { name: mode } });
  let body;
  try { body = response.json(); } catch (_) {}
  const result = classifyResponse(response.status, body);
  counters[result].add(1);
  latency.add(response.timings.duration);
  (result === "accepted" ? acceptedLatency : rejectedLatency).add(response.timings.duration);
  const allowed = result === "accepted"
    || (mode === "sold_out" && result === "sold_out")
    || (mode === "purchase_limit" && result === "business_rejected" && body.code === 60009)
    || (mode === "rate_limit" && result === "rate_limited");
  check(response, {
    "response matches scenario": () => allowed,
    "accepted response contains order ID": () => result !== "accepted" || Boolean(body.data?.order_id),
  });
}

export function handleSummary(data) {
  const value = (name, key) => data.metrics[name]?.values?.[key] || 0;
  const summary = {
    mode, campaign_id: metadata.campaign_id, iterations, target_rate: rate, duration,
    http_requests: value("http_reqs", "count"), http_rate: value("http_reqs", "rate"),
    outcomes: Object.fromEntries(outcomes.map((name) => [name, value(`scenario_${name}`, "count")])),
    accepted_p99_ms: value("accepted_latency", "p(99)"), rejected_p99_ms: value("rejected_latency", "p(99)"),
    checks: data.metrics.checks?.values, dropped_iterations: value("dropped_iterations", "count"),
  };
  return { [__ENV.K6_SUMMARY]: JSON.stringify(summary, null, 2) + "\n", stdout: JSON.stringify(summary) + "\n" };
}
