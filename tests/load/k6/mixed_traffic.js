/**
 * Gofun 混合流量压测（更接近真实入口）
 *
 * 前置：node tests/load/k6/prepare_rush_fixture_fast.mjs
 * 运行：RATE=400 DURATION=60s k6 run tests/load/k6/mixed_traffic.js
 *
 * 流量模型 v2（读多写少；换城低频，同城列表高频）：
 *   events_list           22%   同城活动列表刷新/翻页
 *   event_detail          25%   场次/票档详情
 *   order_detail           8%   订单详情（无单则回落 list）
 *   orders_list            8%   我的订单
 *   rush_sales_list        6%   抢票列表
 *   rush_execute_ok       12%   合格秒杀下单
 *   login                  4%   登录
 *   normal_order           4%   普通下单
 *   catalog_meta           3%   城市/品类元数据
 *   rush_execute_bad       3%   不合格秒杀（缺条款/超量）
 *   register               3%   注册（唯一用户名）
 *   events_list_switch_city 2%  真正换城市后的列表
 */
import http from "k6/http";
import { check, sleep } from "k6";
import { SharedArray } from "k6/data";
import { Counter, Rate, Trend } from "k6/metrics";
import { uuidv4 } from "https://jslib.k6.io/k6-utils/1.4.0/index.js";

http.setResponseCallback(http.expectedStatuses(200, 201, 400, 401, 409, 429));

const fixturePath = __ENV.K6_FIXTURE || "fixtures/rush_execute.json";
const fixture = JSON.parse(String(open(fixturePath)).replace(/^\uFEFF/, ""));
const baseURL = (__ENV.BASE_URL || fixture.base_url || "http://127.0.0.1:18080/api/v1").replace(
  /\/$/,
  "",
);
const campaignID = String(__ENV.RUSH_CAMPAIGN_ID || fixture.campaign_id);
const eventID = String(__ENV.EVENT_ID || fixture.event_id || "");
const tierID = String(__ENV.TIER_ID || fixture.tier_id || "");
const city = __ENV.CITY || fixture.city || "武汉";
const thinkMs = Number(__ENV.THINK_MS || 0);
const password = __ENV.LOAD_PASSWORD || "123456";

const users = new SharedArray("mixed_users", () => {
  if (!Array.isArray(fixture.users) || fixture.users.length === 0) {
    throw new Error(`fixture has no users: ${fixturePath}`);
  }
  return fixture.users;
});

const actionLatency = new Trend("mixed_action_latency", true);
const actionOK = new Rate("mixed_action_ok");
const actionCount = new Counter("mixed_action_count");

const ACTIONS = [
  { name: "events_list", weight: 22 },
  { name: "event_detail", weight: 25 },
  { name: "order_detail", weight: 8 },
  { name: "orders_list", weight: 8 },
  { name: "rush_sales_list", weight: 6 },
  { name: "rush_execute_ok", weight: 12 },
  { name: "login", weight: 4 },
  { name: "normal_order", weight: 4 },
  { name: "catalog_meta", weight: 3 },
  { name: "rush_execute_bad", weight: 3 },
  { name: "register", weight: 3 },
  { name: "events_list_switch_city", weight: 2 },
];
const ALT_CITIES = ["北京", "上海", "广州", "深圳", "成都"];
const totalWeight = ACTIONS.reduce((s, a) => s + a.weight, 0);

const rate = Number(__ENV.RATE || 400);
const duration = __ENV.DURATION || "60s";
const preVUs = Number(__ENV.PRE_VUS || Math.max(rate, 80));
const maxVUs = Number(__ENV.MAX_VUS || Math.max(rate * 2, preVUs));

export const options = {
  summaryTrendStats: ["avg", "min", "med", "p(90)", "p(95)", "p(99)", "max"],
  thresholds: {
    http_req_failed: ["rate<0.15"],
    mixed_action_latency: ["p(99)<2000"],
  },
  scenarios: {
    mixed_rate: {
      executor: "constant-arrival-rate",
      rate,
      timeUnit: "1s",
      duration,
      preAllocatedVUs: preVUs,
      maxVUs,
    },
  },
};

function pickAction() {
  let r = Math.random() * totalWeight;
  for (const a of ACTIONS) {
    r -= a.weight;
    if (r <= 0) return a.name;
  }
  return ACTIONS[ACTIONS.length - 1].name;
}

function pickUser() {
  return users[(__VU * 7919 + __ITER) % users.length];
}

function authHeaders(token, extra = {}) {
  return Object.assign(
    {
      "Content-Type": "application/json",
      Authorization: token,
    },
    extra,
  );
}

function record(name, res, ok) {
  actionLatency.add(res.timings.duration, { action: name });
  actionOK.add(ok, { action: name });
  actionCount.add(1, { action: name });
  check(res, {
    [`${name} accepted`]: () => ok,
  });
}

function doCatalogMeta() {
  const res = http.get(`${baseURL}/catalog/meta`, { tags: { name: "catalog_meta" } });
  record("catalog_meta", res, res.status === 200);
}

function doEventsList(homeCity = true) {
  const page = (__ITER % 3) + 1;
  const qCity = homeCity
    ? city
    : ALT_CITIES[(__VU + __ITER) % ALT_CITIES.length];
  const name = homeCity ? "events_list" : "events_list_switch_city";
  const res = http.get(
    `${baseURL}/events?city=${encodeURIComponent(qCity)}&page=${page}&page_size=20`,
    { tags: { name } },
  );
  record(name, res, res.status === 200);
}

function doEventDetail() {
  if (!eventID) {
    doEventsList(true);
    return;
  }
  const res = http.get(`${baseURL}/events/${eventID}`, { tags: { name: "event_detail" } });
  record("event_detail", res, res.status === 200);
}

function doRushSalesList() {
  const res = http.get(`${baseURL}/rush-sales?page=1&page_size=20`, {
    tags: { name: "rush_sales_list" },
  });
  record("rush_sales_list", res, res.status === 200);
}

function doOrdersList() {
  const user = pickUser();
  const res = http.get(`${baseURL}/orders?page=1&page_size=10`, {
    headers: authHeaders(user.token),
    tags: { name: "orders_list" },
  });
  record("orders_list", res, res.status === 200);
  return res;
}

function doOrderDetail() {
  const user = pickUser();
  const list = http.get(`${baseURL}/orders?page=1&page_size=5`, {
    headers: authHeaders(user.token),
    tags: { name: "orders_list_for_detail" },
  });
  let orderID = null;
  try {
    const body = list.json();
    const items = body?.data?.list || body?.data?.items || [];
    if (items.length > 0) orderID = String(items[0].id || items[0].order_id || "");
  } catch (_) {}
  if (!orderID) {
    record("order_detail", list, list.status === 200);
    return;
  }
  const res = http.get(`${baseURL}/orders/${orderID}`, {
    headers: authHeaders(user.token),
    tags: { name: "order_detail" },
  });
  record("order_detail", res, res.status === 200);
}

function doRushExecuteOK() {
  const user = pickUser();
  const res = http.post(
    `${baseURL}/rush-sales/${campaignID}/execute`,
    JSON.stringify({
      quantity: 1,
      contact_name: "混合压测",
      contact_phone: "13900139000",
      terms_accepted: true,
      attendees: [],
    }),
    {
      headers: authHeaders(user.token, { "X-Idempotency-Key": uuidv4() }),
      tags: { name: "rush_execute_ok" },
    },
  );
  record("rush_execute_ok", res, [200, 400, 409, 429].includes(res.status));
}

function doRushExecuteBad() {
  const user = pickUser();
  // 不合格：不接受条款 + 超量，期望 4xx
  const res = http.post(
    `${baseURL}/rush-sales/${campaignID}/execute`,
    JSON.stringify({
      quantity: 999,
      contact_name: "不合格",
      contact_phone: "13900139000",
      terms_accepted: false,
      attendees: [],
    }),
    {
      headers: authHeaders(user.token, { "X-Idempotency-Key": uuidv4() }),
      tags: { name: "rush_execute_bad" },
    },
  );
  record("rush_execute_bad", res, [400, 409, 422].includes(res.status));
}

function doNormalOrder() {
  if (!tierID) {
    doEventDetail();
    return;
  }
  const user = pickUser();
  const res = http.post(
    `${baseURL}/orders`,
    JSON.stringify({
      ticket_tier_id: tierID,
      quantity: 1,
      contact_name: "普通下单",
      contact_phone: "13800138000",
      terms_accepted: true,
      attendees: [],
    }),
    {
      headers: authHeaders(user.token, { "X-Idempotency-Key": uuidv4() }),
      tags: { name: "normal_order" },
    },
  );
  record("normal_order", res, [200, 400, 409, 429].includes(res.status));
}

function doLogin() {
  const user = pickUser();
  const username = user.username || `user_${user.userID}`;
  const res = http.post(
    `${baseURL}/login`,
    JSON.stringify({ username, password }),
    { headers: { "Content-Type": "application/json" }, tags: { name: "login" } },
  );
  // fast-fixture users use a non-bcrypt placeholder hash; login may fail.
  // Treat 200 as success; 401 still counts as "handled" for traffic shape.
  record("login", res, [200, 401].includes(res.status));
}

function doRegister() {
  const username = `mx_${Date.now().toString(36)}_${__VU}_${__ITER}`.slice(0, 20);
  const res = http.post(
    `${baseURL}/register`,
    JSON.stringify({ username, password }),
    { headers: { "Content-Type": "application/json" }, tags: { name: "register" } },
  );
  record("register", res, [200, 201, 400, 409].includes(res.status));
}

export default function () {
  const action = pickAction();
  switch (action) {
    case "catalog_meta":
      doCatalogMeta();
      break;
    case "events_list":
      doEventsList(true);
      break;
    case "events_list_switch_city":
      doEventsList(false);
      break;
    case "event_detail":
      doEventDetail();
      break;
    case "rush_sales_list":
      doRushSalesList();
      break;
    case "orders_list":
      doOrdersList();
      break;
    case "order_detail":
      doOrderDetail();
      break;
    case "rush_execute_ok":
      doRushExecuteOK();
      break;
    case "rush_execute_bad":
      doRushExecuteBad();
      break;
    case "normal_order":
      doNormalOrder();
      break;
    case "login":
      doLogin();
      break;
    case "register":
      doRegister();
      break;
    default:
      doEventsList(true);
  }
  if (thinkMs > 0) sleep(thinkMs / 1000);
}

export function handleSummary(data) {
  const httpMetrics = data.metrics.http_req_duration || {};
  const values = httpMetrics.values || {};
  const byAction = {};
  const actionLatency = {};
  for (const [name, metric] of Object.entries(data.metrics || {})) {
    // Prefer explicit action counter; fall back to http_reqs name tags.
    let key = null;
    if (name.startsWith("mixed_action_count{")) {
      const m = name.match(/action="([^"]+)"/);
      if (m) key = m[1];
    } else if (name.startsWith("http_reqs{") && name.includes("name=")) {
      const m = name.match(/name="([^"]+)"/);
      if (m) key = m[1];
    }
    if (key) byAction[key] = (byAction[key] || 0) + (metric.values?.count || 0);

    if (name.startsWith("http_req_duration{") && name.includes("name=")) {
      const m = name.match(/name="([^"]+)"/);
      if (m) {
        const v = metric.values || {};
        actionLatency[m[1]] = {
          p50_ms: v["p(50)"] ?? v.med,
          p95_ms: v["p(95)"],
          p99_ms: v["p(99)"],
          avg_ms: v.avg,
          count: data.metrics[`http_reqs{name="${m[1]}"}`]?.values?.count,
        };
      }
    }
  }
  const summary = {
    scenario: "k6_mixed_traffic",
    base_url: baseURL,
    campaign_id: campaignID,
    event_id: eventID,
    tier_id: tierID,
    city,
    target_rate: rate,
    duration,
    weights: Object.fromEntries(ACTIONS.map((a) => [a.name, a.weight])),
    http_reqs: data.metrics.http_reqs?.values?.count || 0,
    http_req_rate: data.metrics.http_reqs?.values?.rate || 0,
    p50_ms: values["p(50)"] ?? values.med,
    p95_ms: values["p(95)"],
    p99_ms: values["p(99)"],
    avg_ms: values.avg,
    mixed_action_ok_rate: data.metrics.mixed_action_ok?.values?.rate,
    action_counts: byAction,
    action_latency: actionLatency,
    http_req_failed_rate: data.metrics.http_req_failed?.values?.rate || 0,
  };
  return {
    stdout: `${JSON.stringify(summary, null, 2)}\n`,
    [__ENV.K6_SUMMARY || "tests/load/results/k6-mixed-traffic-summary.json"]: `${JSON.stringify(summary, null, 2)}\n`,
  };
}
