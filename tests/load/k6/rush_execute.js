/**
 * Gofun 抢票入口压测（k6）
 *
 * 前置：
 *   node tests/load/k6/prepare_rush_fixture.mjs
 *
 * 运行：
 *   k6 run tests/load/k6/rush_execute.js
 *
 * 常用环境变量：
 *   K6_FIXTURE     夹具路径（默认 tests/load/k6/fixtures/rush_execute.json）
 *   BASE_URL       覆盖夹具里的 base_url
 *   VUS            虚拟用户数（默认 50；RATE>0 时仅作预分配参考）
 *   DURATION       持续时间（默认 30s）
 *   RATE           若 >0 则用 constant-arrival-rate，单位 req/s（优先于 VUS/RAMP）
 *   LOAD_PROFILE   peak：150/s 120s → 500/s 10s → 50/s 60s（优先于 RATE）
 *                  soak：150/s 10m → 500/s 10s → 150/s 至总时长 25m
 *   PRE_VUS        RATE 模式预分配 VU（默认 max(RATE, 50)）
 *   MAX_VUS        RATE 模式最大 VU（默认 max(RATE*2, PRE_VUS)）
 *   RAMP_VUS       若设置则走阶梯：ramp -> hold -> ramp-down（忽略 DURATION 的恒定 VU）
 *   RAMP_UP        爬升时间（默认 10s）
 *   HOLD           平台时间（默认 30s）
 *   RAMP_DOWN      下降时间（默认 10s）
 */
import http from "k6/http";
import exec from "k6/execution";
import { check, sleep } from "k6";
import { SharedArray } from "k6/data";
import { Counter, Rate, Trend } from "k6/metrics";
import { uuidv4 } from "https://jslib.k6.io/k6-utils/1.4.0/index.js";
import { classifyResponse } from "./business_result.mjs";

// 售罄/限购/限流算业务响应，不计入 http_req_failed。
http.setResponseCallback(http.expectedStatuses(200, 400, 409, 429));

const fixturePath = __ENV.K6_FIXTURE || "fixtures/rush_execute.json";
const fixture = new SharedArray("rush_metadata", () => {
  const { users, ...metadata } = JSON.parse(open(fixturePath));
  return [metadata];
})[0];
const baseURL = (__ENV.BASE_URL || fixture.base_url || "http://127.0.0.1:18080/api/v1").replace(
  /\/$/,
  "",
);
const campaignID = String(__ENV.RUSH_CAMPAIGN_ID || fixture.campaign_id);
const thinkMs = Number(__ENV.THINK_MS || 0);

const users = new SharedArray("rush_users", () => {
  const data = JSON.parse(open(fixturePath));
  if (!Array.isArray(data.users) || data.users.length === 0) {
    throw new Error(`fixture has no users: ${fixturePath}`);
  }
  return data.users;
});

const executeLatency = new Trend("rush_execute_latency", true);
const executeSuccess = new Rate("rush_execute_success");
const executeSoldOut = new Counter("rush_execute_sold_out");
const executeRejected = new Counter("rush_execute_rejected");
const executeBusinessRejected = new Counter("rush_execute_business_rejected");
const executeRateLimited = new Counter("rush_execute_rate_limited");
const executeServerError = new Counter("rush_execute_server_error");
const executeServerErrorStatus = new Counter("rush_execute_server_error_status");
const executeTransportError = new Counter("rush_execute_transport_error");
const executeUnexpectedStatus = new Counter("rush_execute_unexpected_status");
let transportErrorLogs = 0;

const vus = Number(__ENV.VUS || 50);
const duration = __ENV.DURATION || "30s";
const rampVUs = Number(__ENV.RAMP_VUS || 0);
const rate = Number(__ENV.RATE || 0);
const preVUs = Number(__ENV.PRE_VUS || Math.max(rate, 50));
const maxVUs = Number(__ENV.MAX_VUS || Math.max(rate * 2, preVUs));

const commonOptions = {
  summaryTrendStats: ["avg", "min", "med", "p(90)", "p(95)", "p(99)", "max"],
  thresholds: {
    http_req_failed: ["rate<0.05"],
    rush_execute_latency: ["p(99)<1000"],
  },
};

const peakProfile = __ENV.LOAD_PROFILE === "peak";
const soakProfile = __ENV.LOAD_PROFILE === "soak";
const phasedProfile = peakProfile || soakProfile;
const phases = soakProfile ? [
  { name: "steady_before", rate: 150, seconds: 600, start: 0 },
  { name: "burst", rate: 500, seconds: 10, start: 600 },
  { name: "steady_after", rate: 150, seconds: 890, start: 610 },
] : [
  { name: "steady", rate: 150, seconds: 120, start: 0 },
  { name: "burst", rate: 500, seconds: 10, start: 120 },
  { name: "recovery", rate: 50, seconds: 60, start: 130 },
];
const phaseMetrics = Object.fromEntries((phasedProfile ? phases : []).map(({ name }) => [name, {
  requests: new Counter(`phase_${name}_requests`),
  accepted: new Counter(`phase_${name}_accepted`),
  limited: new Counter(`phase_${name}_limited`),
  acceptedLatency: new Trend(`phase_${name}_accepted_latency`, true),
}]));

const minuteMetrics = Array.from({ length: soakProfile ? 25 : 0 }, (_, i) => ({
  accepted: new Counter(`minute_${i}_accepted`),
  limited: new Counter(`minute_${i}_limited`),
  latency: new Trend(`minute_${i}_accepted_latency`, true),
}));

export const options = phasedProfile ? {
  ...commonOptions,
  scenarios: Object.fromEntries(phases.map(p => [p.name, {
    executor: "constant-arrival-rate", rate: p.rate, timeUnit: "1s",
    duration: `${p.seconds}s`, startTime: `${p.start}s`,
    preAllocatedVUs: 150, maxVUs: 800, gracefulStop: "10s",
  }])),
} :
  rate > 0
    ? {
        ...commonOptions,
        scenarios: {
          rush_rate: {
            executor: "constant-arrival-rate",
            rate,
            timeUnit: "1s",
            duration,
            preAllocatedVUs: preVUs,
            maxVUs,
          },
        },
      }
    : rampVUs > 0
      ? {
          ...commonOptions,
          scenarios: {
            rush_ramp: {
              executor: "ramping-vus",
              startVUs: 0,
              stages: [
                { duration: __ENV.RAMP_UP || "10s", target: rampVUs },
                { duration: __ENV.HOLD || "30s", target: rampVUs },
                { duration: __ENV.RAMP_DOWN || "10s", target: 0 },
              ],
              gracefulRampDown: "5s",
            },
          },
        }
      : {
          ...commonOptions,
          vus,
          duration,
        };

export function setup() {
  return {
    baseURL,
    campaignID,
    userCount: users.length,
    startedAt: Date.now(),
  };
}

export default function (data) {
  // 每轮换用户，避免单用户 20 次限购把入口成功率打穿。
  const userIndex = soakProfile
    ? (exec.scenario.iterationInTest + phases.find(p => p.name === exec.scenario.name).start * 150) % users.length
    : (__VU * 7919 + __ITER) % users.length;
  const user = users[userIndex];
  const url = `${baseURL}/rush-sales/${campaignID}/execute`;
  const res = http.post(
    url,
    JSON.stringify({
      quantity: 1,
      contact_name: "k6用户",
      contact_phone: "13900139000",
      terms_accepted: true,
      attendees: [],
    }),
    {
      headers: {
        "Content-Type": "application/json",
        Authorization: user.token,
        "X-Idempotency-Key": uuidv4(),
      },
      tags: { name: "rush_execute" },
    },
  );

  executeLatency.add(res.timings.duration);

  let body;
  try { body = res.json(); } catch (_) {}
  const result = classifyResponse(res.status, body);
  const okBiz = result === "accepted";
  const soldOut = result === "sold_out";
  const rejected = res.status === 429 || res.status >= 500;

  if (phasedProfile) {
    const m = phaseMetrics[exec.scenario.name];
    m.requests.add(1);
    m.accepted.add(okBiz ? 1 : 0);
    m.limited.add(result === "rate_limited" ? 1 : 0);
    if (okBiz) m.acceptedLatency.add(res.timings.duration);
  }
  if (soakProfile) {
    const minute = Math.min(24, Math.floor((Date.now() - data.startedAt) / 60000));
    const m = minuteMetrics[minute];
    m.accepted.add(okBiz ? 1 : 0);
    m.limited.add(result === "rate_limited" ? 1 : 0);
    if (okBiz) m.latency.add(res.timings.duration);
  }
  executeSuccess.add(okBiz);
  if (soldOut) {
    executeSoldOut.add(1);
    executeBusinessRejected.add(1);
  } else if (result === "business_rejected") {
    executeBusinessRejected.add(1);
  } else if (result === "rate_limited") {
    executeRateLimited.add(1);
  } else if (res.status >= 500) {
    executeServerError.add(1);
    executeServerErrorStatus.add(1, { status: String(res.status) });
    console.error(
      `[rush_execute_server_error] status=${res.status} body=${String(res.body || "").slice(0, 512)}`,
    );
  } else if (res.status === 0 || res.error || res.error_code) {
    executeTransportError.add(1);
    if (transportErrorLogs < 100) {
      transportErrorLogs += 1;
      console.error(
        `[rush_execute_transport_error] status=${res.status} error_code=${res.error_code || ""} error=${String(res.error || "")}`,
      );
    }
  } else if (result === "unexpected") {
    executeUnexpectedStatus.add(1);
  }
  if (rejected) executeRejected.add(1);

  check(res, {
    "status is 200/400/409/429": (r) => [200, 400, 409, 429].includes(r.status),
  });

  if (thinkMs > 0) {
    sleep(thinkMs / 1000);
  }
}

export function handleSummary(data) {
  const httpMetrics = data.metrics.http_req_duration || {};
  const values = httpMetrics.values || {};
  const summary = {
    scenario: "k6_rush_execute",
    phases: phasedProfile ? phases.map(p => ({ ...p,
      requests: data.metrics[`phase_${p.name}_requests`]?.values?.count || 0,
      accepted: data.metrics[`phase_${p.name}_accepted`]?.values?.count || 0,
      rate_limited: data.metrics[`phase_${p.name}_limited`]?.values?.count || 0,
      accepted_latency_ms: data.metrics[`phase_${p.name}_accepted_latency`]?.values,
    })) : undefined,
    minutes: soakProfile ? minuteMetrics.map((_, minute) => ({ minute,
      accepted: data.metrics[`minute_${minute}_accepted`]?.values?.count || 0,
      rate_limited: data.metrics[`minute_${minute}_limited`]?.values?.count || 0,
      accepted_latency_ms: data.metrics[`minute_${minute}_accepted_latency`]?.values,
    })) : undefined,
    base_url: baseURL,
    campaign_id: campaignID,
    fixture: fixturePath,
    users: users.length,
    http_reqs: data.metrics.http_reqs?.values?.count || 0,
    http_req_rate: data.metrics.http_reqs?.values?.rate || 0,
    p50_ms: values["p(50)"] ?? values.med,
    p90_ms: values["p(90)"],
    p95_ms: values["p(95)"],
    p99_ms: values["p(99)"],
    avg_ms: values.avg,
    rush_execute_success_rate: data.metrics.rush_execute_success?.values?.rate,
    rush_execute_sold_out: data.metrics.rush_execute_sold_out?.values?.count || 0,
    rush_execute_rejected: data.metrics.rush_execute_rejected?.values?.count || 0,
    rush_execute_business_rejected:
      data.metrics.rush_execute_business_rejected?.values?.count || 0,
    rush_execute_rate_limited:
      data.metrics.rush_execute_rate_limited?.values?.count || 0,
    rush_execute_server_error:
      data.metrics.rush_execute_server_error?.values?.count || 0,
    rush_execute_server_error_statuses: collectTaggedCounts(
      data.metrics,
      "rush_execute_server_error_status",
    ),
    rush_execute_transport_error:
      data.metrics.rush_execute_transport_error?.values?.count || 0,
    rush_execute_unexpected_status:
      data.metrics.rush_execute_unexpected_status?.values?.count || 0,
    http_req_failed_rate: data.metrics.http_req_failed?.values?.rate || 0,
    dropped_iterations: data.metrics.dropped_iterations?.values?.count || 0,
    checks: data.metrics.checks?.values,
  };
  return {
    stdout: `${JSON.stringify(summary, null, 2)}\n`,
    [__ENV.K6_SUMMARY || "tests/load/results/k6-rush-execute-summary.json"]: `${JSON.stringify(summary, null, 2)}\n`,
  };
}

function collectTaggedCounts(allMetrics, prefix) {
  const result = {};
  for (const [name, metric] of Object.entries(allMetrics || {})) {
    if (name === prefix || !name.startsWith(`${prefix}{`)) continue;
    result[name] = metric.values?.count || 0;
  }
  return result;
}
