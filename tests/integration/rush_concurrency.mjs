/**
 * 抢票并发验收套件（面试证据）：
 * 1) 同用户同幂等键并发 → 只一单
 * 2) 同用户超限购并发 → 成功数 ≤ per_user_limit
 * 3) 多用户抢小库存 → 成功数 = total_quota，其余失败
 *
 * 用法（gofun-it backend 已启动，admin/admin123 可用）:
 *   $env:BASE_URL='http://127.0.0.1:18080/api/v1'
 *   node tests/integration/rush_concurrency.mjs
 */
import { randomUUID } from "node:crypto";
import { bootstrapRushCampaign, executeRush } from "./lib/catalog_bootstrap.mjs";
import { assert, http, registerAndLogin } from "./lib/http.mjs";

function isSuccess(res) {
  return res.status === 200 && !!res.data?.data?.order_id;
}

function isBusinessReject(res) {
  // 售罄 / 限购 / 不可用 等业务失败，不应 5xx
  return res.status >= 400 && res.status < 500;
}

async function caseIdempotencyConcurrent() {
  const { campaignID } = await bootstrapRushCampaign({
    totalQuota: 5,
    perUserLimit: 2,
    label: "idem",
  });
  const user = await registerAndLogin(`i${randomUUID().replace(/-/g, "").slice(0, 12)}`);
  const key = randomUUID();

  const shots = await Promise.all(
    Array.from({ length: 8 }, () => executeRush(user.token, campaignID, key, "幂等并发")),
  );
  const ok = shots.filter(isSuccess);
  const orderIDs = new Set(ok.map((item) => String(item.data.data.order_id)));
  assert(ok.length >= 1, `idempotency: expected ≥1 success, got ${JSON.stringify(shots.map((s) => s.status))}`);
  assert(orderIDs.size === 1, `idempotency: expected 1 order id, got ${[...orderIDs]}`);

  const list = await http("GET", "/orders?page=1&page_size=50", { token: user.token });
  const rows = (list.data?.data?.list || []).filter((o) => String(o.id) === [...orderIDs][0]);
  assert(rows.length === 1, `idempotency: list should show one row, got ${rows.length}`);

  return {
    name: "idempotency_concurrent",
    ok: true,
    campaign_id: campaignID,
    order_id: [...orderIDs][0],
    attempts: shots.length,
    successes: ok.length,
  };
}

async function casePerUserLimitConcurrent() {
  const perUserLimit = 1;
  const { campaignID } = await bootstrapRushCampaign({
    totalQuota: 10,
    perUserLimit,
    label: "limit",
  });
  const user = await registerAndLogin(`l${randomUUID().replace(/-/g, "").slice(0, 12)}`);

  const shots = await Promise.all(
    Array.from({ length: 6 }, () =>
      executeRush(user.token, campaignID, randomUUID(), "限购并发"),
    ),
  );
  const ok = shots.filter(isSuccess);
  const rejected = shots.filter(isBusinessReject);
  assert(
    ok.length === perUserLimit,
    `per_user_limit: want ${perUserLimit} success, got ${ok.length}; statuses=${shots.map((s) => s.status)}`,
  );
  assert(
    rejected.length === shots.length - ok.length,
    `per_user_limit: non-success should be 4xx, got ${JSON.stringify(shots.map((s) => ({ status: s.status, msg: s.data?.msg })))}`,
  );

  return {
    name: "per_user_limit_concurrent",
    ok: true,
    campaign_id: campaignID,
    successes: ok.length,
    rejects: rejected.length,
  };
}

async function caseStockRace() {
  const totalQuota = 3;
  const racers = 12;
  const { campaignID } = await bootstrapRushCampaign({
    totalQuota,
    perUserLimit: 1,
    tierQuota: 20,
    label: "stock",
  });

  const users = [];
  for (let i = 0; i < racers; i += 1) {
    users.push(await registerAndLogin(`s${i}${randomUUID().replace(/-/g, "").slice(0, 10)}`.slice(0, 20)));
  }

  const shots = await Promise.all(
    users.map((u, idx) =>
      executeRush(u.token, campaignID, randomUUID(), `抢库存${idx}`),
    ),
  );
  const ok = shots.filter(isSuccess);
  const rejected = shots.filter(isBusinessReject);
  assert(
    ok.length === totalQuota,
    `stock_race: want ${totalQuota} successes, got ${ok.length}; detail=${JSON.stringify(shots.map((s) => ({ status: s.status, msg: s.data?.msg })))}`,
  );
  assert(
    ok.length + rejected.length === racers,
    `stock_race: every attempt should resolve 2xx-success or 4xx, got mixed`,
  );

  return {
    name: "stock_race",
    ok: true,
    campaign_id: campaignID,
    racers,
    successes: ok.length,
    rejects: rejected.length,
  };
}

const results = [];
for (const run of [caseIdempotencyConcurrent, casePerUserLimitConcurrent, caseStockRace]) {
  // 顺序跑：每个 case 自建 campaign，避免互相抢同一票池。
  results.push(await run());
}

console.log(JSON.stringify({ ok: true, cases: results }, null, 2));
