/**
 * 为 k6 准备抢票入口压测夹具：建 campaign + 预注册用户，写出 JSON。
 *
 * 用法：
 *   $env:BASE_URL='http://127.0.0.1:18080/api/v1'
 *   $env:K6_USERS='500'
 *   $env:RUSH_TOTAL_QUOTA='100000'
 *   $env:RUSH_PER_USER_LIMIT='100'
 *   node tests/load/k6/prepare_rush_fixture.mjs
 *
 * 输出默认：tests/load/k6/fixtures/rush_execute.json
 */
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { bootstrapRushCampaign } from "../../integration/lib/catalog_bootstrap.mjs";
import { Metrics, ensureUser } from "../lib/load_common.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..");
const userCount = Number(process.env.K6_USERS || 500);
const setupConcurrency = Number(process.env.SETUP_CONCURRENCY || 20);
const totalQuota = Number(process.env.RUSH_TOTAL_QUOTA || 100000);
// 活动单笔限购上限为 20；吞吐压测靠「多用户轮转」而不是单用户无限买。
const perUserLimit = Math.min(Number(process.env.RUSH_PER_USER_LIMIT || 20), 20);
const outPath =
  process.env.K6_FIXTURE ||
  join(root, "tests", "load", "k6", "fixtures", "rush_execute.json");

if (!Number.isInteger(userCount) || userCount <= 0) {
  throw new Error("K6_USERS must be a positive integer");
}

await mkdir(dirname(outPath), { recursive: true });

console.error(`bootstrapping rush campaign quota=${totalQuota} per_user=${perUserLimit}...`);
const campaign = await bootstrapRushCampaign({
  totalQuota,
  perUserLimit,
  tierQuota: Math.max(totalQuota, 64),
  maxTicketsPerOrder: perUserLimit,
  label: process.env.RUSH_LABEL || "k6-rush",
});

console.error(`preparing ${userCount} users (concurrency=${setupConcurrency})...`);
const metrics = new Metrics("k6_fixture_setup");
const users = new Array(userCount);
for (let offset = 0; offset < userCount; offset += setupConcurrency) {
  const slice = Array.from(
    { length: Math.min(setupConcurrency, userCount - offset) },
    (_, i) => offset + i,
  );
  await Promise.all(
    slice.map(async (i) => {
      const username = `k6u${String(i).padStart(5, "0")}`;
      const token = await ensureUser(username, process.env.LOAD_PASSWORD || "123456", metrics);
      users[i] = { username, token };
    }),
  );
  console.error(`  users ${Math.min(offset + setupConcurrency, userCount)}/${userCount}`);
}

const fixture = {
  prepared_at: new Date().toISOString(),
  base_url: process.env.BASE_URL || "http://127.0.0.1:18080/api/v1",
  campaign_id: campaign.campaignID,
  event_id: campaign.eventID,
  tier_id: campaign.tierID,
  total_quota: campaign.totalQuota,
  per_user_limit: campaign.perUserLimit,
  users,
};

await writeFile(outPath, `${JSON.stringify(fixture, null, 2)}\n`, "utf8");
console.log(
  JSON.stringify(
    {
      fixture: outPath,
      campaign_id: fixture.campaign_id,
      users: users.length,
      total_quota: fixture.total_quota,
      per_user_limit: fixture.per_user_limit,
      base_url: fixture.base_url,
    },
    null,
    2,
  ),
);
