/**
 * 库存分桶验收（需 backend inventory.buckets_enabled=true）：
 * 1) 小库�?(<min_quota_to_bucket) �?MySQL �?1 �?bucket �?
 * 2) 大库�?�?bucket_count �?bucket 行；列表余量 = SUM
 * 3) 抢票个人限购仍全局
 *
 * 可选：MYSQL_CONTAINER / MYSQL_* 用于 docker exec 查表行数�?
 */
import { assert, http, purchaseBody, registerAndLogin } from "./lib/http.mjs";
import { bootstrapRushCampaign } from "./lib/catalog_bootstrap.mjs";
import { execFileSync } from "node:child_process";

const base = process.env.BASE_URL || "http://127.0.0.1:18080/api/v1";
process.env.BASE_URL = base;
const mysqlContainer = process.env.MYSQL_CONTAINER || "gofun-capacity-mysql-1";
const mysqlUser = process.env.MYSQL_USER || "fuchang";
const mysqlPassword = process.env.MYSQL_PASSWORD || "fuchang-it-mysql";
const mysqlDB = process.env.MYSQL_DB || "fuchang_ticketing_it";
const expectBuckets = Number(process.env.EXPECT_BUCKET_COUNT || 8);

function mysqlCount(sql) {
  const out = execFileSync(
    "docker",
    [
      "exec",
      mysqlContainer,
      "mysql",
      `-u${mysqlUser}`,
      `-p${mysqlPassword}`,
      mysqlDB,
      "-N",
      "-e",
      sql,
    ],
    { encoding: "utf8" },
  ).trim();
  return Number(out);
}

const small = await bootstrapRushCampaign({
  totalQuota: 3,
  perUserLimit: 1,
  tierQuota: 10,
  label: "bucket-small",
});
const large = await bootstrapRushCampaign({
  totalQuota: 80,
  perUserLimit: 1,
  tierQuota: 200,
  label: "bucket-large",
});

const smallTierBuckets = mysqlCount(
  `SELECT COUNT(*) FROM ticket_tier_bucket WHERE tier_id=${small.tierID}`,
);
const largeTierBuckets = mysqlCount(
  `SELECT COUNT(*) FROM ticket_tier_bucket WHERE tier_id=${large.tierID}`,
);
const smallRushBuckets = mysqlCount(
  `SELECT COUNT(*) FROM rush_campaign_bucket WHERE campaign_id=${small.campaignID}`,
);
const largeRushBuckets = mysqlCount(
  `SELECT COUNT(*) FROM rush_campaign_bucket WHERE campaign_id=${large.campaignID}`,
);

assert(smallTierBuckets === 1, `small tier buckets want 1 got ${smallTierBuckets}`);
assert(smallRushBuckets === 1, `small rush buckets want 1 got ${smallRushBuckets}`);
assert(
  largeTierBuckets === expectBuckets,
  `large tier buckets want ${expectBuckets} got ${largeTierBuckets}`,
);
assert(
  largeRushBuckets === expectBuckets,
  `large rush buckets want ${expectBuckets} got ${largeRushBuckets}`,
);

const list = await http("GET", "/rush-sales");
assert(list.ok, `list rush failed: ${JSON.stringify(list.data)}`);
const largeView = (list.data?.data || []).find((c) => String(c.id) === String(large.campaignID));
assert(largeView, "large campaign not listed");
assert(
  Number(largeView.remaining_quota) === 80,
  `large remaining expected 80 got ${largeView.remaining_quota}`,
);

const buyer = await registerAndLogin(`b${Date.now().toString(36)}`.slice(0, 20));
const body = purchaseBody("分桶买家");
const first = await http("POST", `/rush-sales/${large.campaignID}/execute`, {
  token: buyer.token,
  headers: { "X-Idempotency-Key": `idem-a-${Date.now()}` },
  body,
});
assert(first.ok, `first execute failed: ${JSON.stringify(first.data)}`);
const second = await http("POST", `/rush-sales/${large.campaignID}/execute`, {
  token: buyer.token,
  headers: { "X-Idempotency-Key": `idem-b-${Date.now()}` },
  body,
});
assert(!second.ok, `second execute should hit per-user limit: ${JSON.stringify(second.data)}`);

console.log(
  JSON.stringify({
    ok: true,
    small: { tier_buckets: smallTierBuckets, rush_buckets: smallRushBuckets },
    large: { tier_buckets: largeTierBuckets, rush_buckets: largeRushBuckets },
  }),
);
