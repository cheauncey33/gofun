import http from "k6/http";
import { check, sleep } from "k6";

// 配置: 阶梯压测
// 运行方式: k6 run tests/load/seckill_load_test.js

export const options = {
  stages: [
    { duration: "30s", target: 50 },    // 预热: 30秒内涨到50并发
    { duration: "1m", target: 200 },     // 持续: 200并发
    { duration: "30s", target: 500 },    // 峰值: 500并发
    { duration: "30s", target: 0 },      // 冷却: 降到0
  ],
  thresholds: {
    http_req_duration: ["p(95)<500"],   // 95%请求 < 500ms
    http_req_failed: ["rate<0.05"],      // 错误率 < 5%
  },
};

const BASE_URL = "http://127.0.0.1:8080/api/v1";

export default function () {
  // === 1. 登录 ===
  const loginPayload = JSON.stringify({
    username: "testuser" + __VU,
    password: "123456",
  });
  const loginRes = http.post(`${BASE_URL}/login`, loginPayload, {
    headers: { "Content-Type": "application/json" },
  });

  const loginBody = loginRes.json();
  const token = loginBody.data?.token;
  if (!token) {
    // 注册新用户
    const registerPayload = JSON.stringify({
      username: "testuser" + __VU,
      password: "123456",
    });
    const regRes = http.post(`${BASE_URL}/register`, registerPayload, {
      headers: { "Content-Type": "application/json" },
    });
    if (regRes.status !== 200) {
      return;
    }
    // 重新登录
    const reLoginRes = http.post(`${BASE_URL}/login`, loginPayload, {
      headers: { "Content-Type": "application/json" },
    });
    const reBody = reLoginRes.json();
    const reToken = reBody.data?.token;
    if (!reToken) return;
    doBuy(reToken);
  } else {
    doBuy(token);
  }

  sleep(1);
}

function doBuy(token) {
  const authHeaders = {
    "Content-Type": "application/json",
    Authorization: token,
  };

  // === 2. 获取秒杀活动列表 ===
  const listRes = http.get(`${BASE_URL}/seckill/activities?page=1&page_size=5`, {
    headers: authHeaders,
  });

  const activities = listRes.json().data?.list;
  if (!activities || activities.length === 0) {
    // 没有秒杀活动, 试试正常下单
    normalOrder(token);
    return;
  }

  // === 3. 选择第一个活跃活动 ===
  const activity = activities.find((a) => a.status === 1); // SeckillStatusActive
  if (!activity) return;

  // === 4. 获取秒杀令牌 ===
  const tokenRes = http.post(
    `${BASE_URL}/seckill/activities/${activity.id}/token`,
    "{}",
    { headers: authHeaders }
  );
  const tokenBody = tokenRes.json();
  const seckillToken = tokenBody.data?.token;
  if (!seckillToken) return;

  // === 5. 执行秒杀 ===
  const execPayload = JSON.stringify({
    token: seckillToken,
    quantity: 1,
  });
  const execRes = http.post(
    `${BASE_URL}/seckill/activities/${activity.id}/execute`,
    execPayload,
    { headers: authHeaders }
  );

  check(execRes, {
    "seckill success": (r) => r.status === 200,
    "seckill sold out": (r) => r.status !== 200,
  });
}

function normalOrder(token) {
  const authHeaders = {
    "Content-Type": "application/json",
    Authorization: token,
  };

  // 获取商品列表
  const productRes = http.get(`${BASE_URL}/products?page=1&page_size=5`, {
    headers: authHeaders,
  });
  const products = productRes.json().data?.list;
  if (!products || products.length === 0) return;

  // 下单
  const orderPayload = JSON.stringify({
    items: [{ product_id: products[0].id, num: 1 }],
  });
  http.post(`${BASE_URL}/orders`, orderPayload, { headers: authHeaders });
}
