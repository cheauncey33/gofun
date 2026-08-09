/**
 * 多热点造数：直接写 MySQL�? 可选分桶行）并预热 Redis�?
 * 比逐条 HTTP bootstrap 快一个数量级，适合 HOTSPOTS=8/16 扫描�?
 */
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";

function env(name, fallback = "") {
  const value = process.env[name];
  return value == null || value === "" ? fallback : value;
}

function splitQuotaEvenly(total, n) {
  const out = Array.from({ length: n }, () => Math.floor(total / n));
  for (let i = 0; i < total % n; i += 1) out[i] += 1;
  return out;
}

function effectiveBucketCount(totalQuota, bucketsEnabled, bucketCount, minQuota) {
  if (!bucketsEnabled) return 1;
  if (totalQuota < minQuota) return 1;
  return Math.max(1, bucketCount);
}

function mysqlExec(sql, { mysqlContainer, mysqlUser, mysqlPassword, mysqlDB }) {
  const raw = execFileSync(
    "docker",
    [
      "exec",
      "-i",
      mysqlContainer,
      "mysql",
      `-u${mysqlUser}`,
      `-p${mysqlPassword}`,
      mysqlDB,
      "-N",
      "-e",
      sql,
    ],
    { encoding: "utf8", windowsHide: true },
  );
  const lines = raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith("mysql:"));
  return lines.at(-1) || "";
}

function redisCli(args, { redisContainer, redisPassword }) {
  const cmd = redisPassword
    ? ["exec", redisContainer, "redis-cli", "-a", redisPassword, ...args]
    : ["exec", redisContainer, "redis-cli", ...args];
  return execFileSync("docker", cmd, {
    encoding: "utf8",
    windowsHide: true,
  }).trim();
}

function sqlString(value) {
  return `'${String(value).replace(/\\/g, "\\\\").replace(/'/g, "''")}'`;
}

function sqlDate(offsetMs) {
  const d = new Date(Date.now() + offsetMs);
  const pad = (n) => String(n).padStart(2, "0");
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ` +
    `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}.000`;
}

/**
 * @param {object} opts
 * @param {number} opts.count 热点（活�?票档/抢票）数�?
 * @param {number} opts.quotaPerCampaign 每个 campaign 的剩余票�?
 * @param {number} [opts.perUserLimit]
 * @param {string} [opts.label]
 * @returns {Promise<Array<{campaignID:string,tierID:string,eventID:string,quota:number}>>}
 */
export async function seedMultiHotspotCampaigns({
  count,
  quotaPerCampaign,
  perUserLimit = 1,
  label = "multi-hotspot",
} = {}) {
  if (!Number.isInteger(count) || count <= 0) {
    throw new Error("count must be positive integer");
  }
  if (!Number.isInteger(quotaPerCampaign) || quotaPerCampaign <= 0) {
    throw new Error("quotaPerCampaign must be positive integer");
  }

  const cfg = {
    mysqlContainer: env("MYSQL_CONTAINER", "gofun-capacity-mysql-1"),
    mysqlUser: env("MYSQL_USER", "fuchang"),
    mysqlPassword: env("MYSQL_PASSWORD", "fuchang-it-mysql"),
    mysqlDB: env("MYSQL_DB", "fuchang_ticketing_it"),
    redisContainer: env("REDIS_CONTAINER", "gofun-capacity-redis-1"),
    redisPassword: env("REDIS_PASSWORD", "fuchang-it-redis"),
  };
  const bucketsEnabled = String(env("INVENTORY_BUCKETS_ENABLED", "false")).toLowerCase() === "true";
  const bucketCount = Number(env("INVENTORY_BUCKET_COUNT", "8"));
  const minQuota = Number(env("INVENTORY_MIN_QUOTA_TO_BUCKET", "64"));

  const adminID = mysqlExec(
    "SELECT id FROM user WHERE username='admin' AND delete_time IS NULL LIMIT 1",
    cfg,
  );
  if (!adminID) throw new Error("admin user not found; start capacity stack first");

  const suffix = `${Date.now().toString(36)}${randomUUID().slice(0, 4)}`;
  const slug = `mh${suffix}`.toLowerCase().replace(/[^a-z0-9-]/g, "").slice(0, 32);

  const orgID = mysqlExec(
    `
INSERT INTO organizer (name, slug, contact_name, contact_phone, status, audit_status, create_time, update_time)
VALUES (${sqlString(`${label}-主办�?${suffix}`)}, ${sqlString(slug)}, 'MH', '13800000000', 'active', 'approved', NOW(3), NOW(3));
SELECT LAST_INSERT_ID();
`.trim(),
    cfg,
  );

  mysqlExec(
    `
INSERT INTO organizer_member (organizer_id, user_id, role, status, create_time, update_time)
VALUES (${orgID}, ${adminID}, 'owner', 'active', NOW(3), NOW(3));
`.trim(),
    cfg,
  );

  const venueID = mysqlExec(
    `
INSERT INTO venue (organizer_id, name, city, district, address, timezone, status, create_time, update_time)
VALUES (${orgID}, ${sqlString("多热点压测场�?)}, '武汉', '洪山�?, 'Load Test Rd 1', 'Asia/Shanghai', 'active', NOW(3), NOW(3));
SELECT LAST_INSERT_ID();
`.trim(),
    cfg,
  );

  const startsAt = sqlDate(7 * 24 * 3600 * 1000);
  const endsAt = sqlDate(7 * 24 * 3600 * 1000 + 3 * 3600 * 1000);
  const saleStarts = sqlDate(-60_000);
  const saleEnds = sqlDate(6 * 24 * 3600 * 1000);
  const rushStarts = sqlDate(-60_000);
  const rushEnds = sqlDate(2 * 3600 * 1000);

  const campaigns = [];
  const redisPipe = [];

  for (let i = 0; i < count; i += 1) {
    const eventID = mysqlExec(
      `
INSERT INTO event (
  organizer_id, title, subtitle, category, description, status,
  real_name_required, max_tickets_per_order, published_at, create_time, update_time
) VALUES (
  ${orgID},
  ${sqlString(`${label}-${suffix}-${i}`)},
  'multi-hotspot',
  '压测',
  'seeded for multi-hotspot load',
  'published',
  0,
  6,
  NOW(3),
  NOW(3),
  NOW(3)
);
SELECT LAST_INSERT_ID();
`.trim(),
      cfg,
    );

    const sessionID = mysqlExec(
      `
INSERT INTO event_session (
  event_id, venue_id, starts_at, ends_at, sale_starts_at, sale_ends_at,
  status, version, create_time, update_time
) VALUES (
  ${eventID}, ${venueID},
  ${sqlString(startsAt)}, ${sqlString(endsAt)},
  ${sqlString(saleStarts)}, ${sqlString(saleEnds)},
  'on_sale', 0, NOW(3), NOW(3)
);
SELECT LAST_INSERT_ID();
`.trim(),
      cfg,
    );

    const tierQuota = Math.max(quotaPerCampaign, quotaPerCampaign);
    const tierID = mysqlExec(
      `
INSERT INTO ticket_tier (
  session_id, name, description, price_cents, total_quota, remaining_quota,
  sold_count, purchase_limit, status, version, create_time, update_time
) VALUES (
  ${sessionID},
  ${sqlString(`票档-${i}`)},
  'multi-hotspot tier',
  12800,
  ${tierQuota},
  ${tierQuota},
  0,
  ${Math.max(perUserLimit, 2)},
  'on_sale',
  0,
  NOW(3),
  NOW(3)
);
SELECT LAST_INSERT_ID();
`.trim(),
      cfg,
    );

    const campaignID = mysqlExec(
      `
INSERT INTO rush_sale_campaign (
  organizer_id, ticket_tier_id, name, rush_price_cents, total_quota, remaining_quota,
  per_user_limit, starts_at, ends_at, status, create_time, update_time
) VALUES (
  ${orgID},
  ${tierID},
  ${sqlString(`直抢-${suffix}-${i}`)},
  6800,
  ${quotaPerCampaign},
  ${quotaPerCampaign},
  ${perUserLimit},
  ${sqlString(rushStarts)},
  ${sqlString(rushEnds)},
  'active',
  NOW(3),
  NOW(3)
);
SELECT LAST_INSERT_ID();
`.trim(),
      cfg,
    );

    const nTier = effectiveBucketCount(tierQuota, bucketsEnabled, bucketCount, minQuota);
    const nRush = effectiveBucketCount(quotaPerCampaign, bucketsEnabled, bucketCount, minQuota);
    if (bucketsEnabled) {
      const tierParts = splitQuotaEvenly(tierQuota, nTier);
      const rushParts = splitQuotaEvenly(quotaPerCampaign, nRush);
      const tierRows = tierParts
        .map(
          (q, bucketNo) =>
            `(${tierID}, ${bucketNo}, ${q}, 0, 0, NOW(3), NOW(3))`,
        )
        .join(",");
      const rushRows = rushParts
        .map(
          (q, bucketNo) =>
            `(${campaignID}, ${bucketNo}, ${q}, 0, NOW(3), NOW(3))`,
        )
        .join(",");
      mysqlExec(
        `
INSERT INTO ticket_tier_bucket (tier_id, bucket_no, remaining_quota, sold_count, version, create_time, update_time)
VALUES ${tierRows};
INSERT INTO rush_campaign_bucket (campaign_id, bucket_no, remaining_quota, version, create_time, update_time)
VALUES ${rushRows};
`.trim(),
        cfg,
      );
      for (let b = 0; b < nTier; b += 1) {
        redisPipe.push(["SET", `fuchang:ticket:stock:${tierID}:${b}`, String(tierParts[b])]);
      }
      for (let b = 0; b < nRush; b += 1) {
        redisPipe.push(["SET", `fuchang:rush:stock:${campaignID}:${b}`, String(rushParts[b])]);
      }
    } else {
      redisPipe.push(["SET", `fuchang:ticket:stock:${tierID}`, String(tierQuota)]);
      redisPipe.push(["SET", `fuchang:rush:stock:${campaignID}`, String(quotaPerCampaign)]);
    }

    campaigns.push({
      campaignID: String(campaignID),
      tierID: String(tierID),
      eventID: String(eventID),
      sessionID: String(sessionID),
      organizerID: String(orgID),
      quota: quotaPerCampaign,
      index: i,
    });
  }

  // redis-cli --pipe 在部分镜像不可用；逐条 SET 对几十个 key 足够快�?
  for (const args of redisPipe) {
    const out = redisCli(args, cfg);
    if (out && !out.includes("OK") && !/^\d+$/.test(out)) {
      // redis-cli -a 会先�?Warning 行；只要最终不是错误即可�?
      if (/ERR|WRONGPASS|NOAUTH/i.test(out)) {
        throw new Error(`redis set failed: ${out}`);
      }
    }
  }

  return {
    organizerID: String(orgID),
    venueID: String(venueID),
    bucketsEnabled,
    campaigns,
  };
}
