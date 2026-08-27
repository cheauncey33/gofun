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
 *   PRE_VUS        RATE 模式预分配 VU（默认 max(RATE, 50)）
 *   MAX_VUS        RATE 模式最大 VU（默认 max(RATE*2, PRE_VUS)）
 *   RAMP_VUS       若设置则走阶梯：ramp -> hold -> ramp-down（忽略 DURATION 的恒定 VU）
 *   RAMP_UP        爬升时间（默认 10s）
 *   HOLD           平台时间（默认 30s）
 *   RAMP_DOWN      下降时间（默认 10s）
 */
import http from "k6/http";
import { check, sleep } from "k6";
import { SharedArray } from "k6/data";
import { Counter, Rate, Trend } from "k6/metrics";
import { uuidv4 } from "https://jslib.k6.io/k6-utils/1.4.0/index.js";

// 售罄/限购/限流算业务响应，不计入 http_req_failed。
http.setResponseCallback(http.expectedStatuses(200, 400, 409, 429));

const fixturePath = __ENV.K6_FIXTURE || "fixtures/rush_execute.json";
const fixture = JSON.parse(open(fixturePath));
const baseURL = (__ENV.BASE_URL || fixture.base_url || "http://127.0.0.1:18080/api/v1").replace(
  /\/$/,
  "",
);
const campaignID = String(__ENV.RUSH_CAMPAIGN_ID || fixture.campaign_id);
const thinkMs = Number(__ENV.THINK_MS || 0);

const users = new SharedArray("rush_users", () => {
  if (!Array.isArray(fixture.users) || fixture.users.length === 0) {
    throw new Error(`fixture has no users: ${fixturePath}`);
  }
  return fixture.users;
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

export const options =
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
  };
}

export default function () {
  // 每轮换用户，避免单用户 20 次限购把入口成功率打穿。
  const user = users[(__VU * 7919 + __ITER) % users.length];
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

  const okBiz = res.status === 200;
  const soldOut = res.status === 400 || res.status === 409;
  const rejected = res.status === 429 || res.status >= 500;

  executeSuccess.add(okBiz);
  if (soldOut) {
    executeSoldOut.add(1);
    executeBusinessRejected.add(1);
  } else if (res.status === 429) {
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
  } else if (res.status !== 200) {
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
