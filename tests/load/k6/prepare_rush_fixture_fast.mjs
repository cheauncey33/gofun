import { createHmac } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";
import { bootstrapRushCampaign } from "../../integration/lib/catalog_bootstrap.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..");
const userCount = Number(process.env.K6_USERS || 2500);
const totalQuota = Number(process.env.RUSH_TOTAL_QUOTA || 50000);
const perUserLimit = 20;
const mysqlContainer = process.env.MYSQL_CONTAINER;
const jwtSecret = process.env.K6_JWT_SECRET || "fuchang-integration-test-secret-only";
const userPrefix = process.env.K6_FAST_USER_PREFIX || `fastk6_${Date.now()}_`;
const outPath =
  process.env.K6_FIXTURE || join(root, "tests", "load", "k6", "fixtures", "rush_execute.json");

if (!Number.isInteger(userCount) || userCount <= 0) throw new Error("K6_USERS must be positive");
if (!mysqlContainer) throw new Error("MYSQL_CONTAINER is required for fast fixture preparation");

const campaign = await bootstrapRushCampaign({
  totalQuota,
  perUserLimit,
  tierQuota: totalQuota,
  maxTicketsPerOrder: perUserLimit,
  label: process.env.RUSH_LABEL || "k6-failure-diagnostic",
});

const passwordHash = "$2a$12$00000000000000000000000000000000000000000000000000000";
const values = [];
for (let i = 0; i < userCount; i += 1) {
  const username = `${userPrefix}${String(i).padStart(5, "0")}`;
  values.push(
    `(NOW(3),NOW(3),NULL,'${username}','${passwordHash}',100,100000,NULL,'','user',NULL)`,
  );
}
const sql =
  "INSERT INTO user (update_time,create_time,delete_time,username,password,balance,balance_cents,phone,avatar_url,role,last_login_at) VALUES " +
  values.join(",") + ";";
execFileSync(
  "docker",
  ["exec", "-i", "-e", "MYSQL_PWD=fuchang-it-mysql", mysqlContainer, "mysql", "-ufuchang", "-N", "-B", "fuchang_ticketing_it"],
  { input: sql, encoding: "utf8", stdio: ["pipe", "pipe", "pipe"] },
);

const rows = execFileSync(
  "docker",
  [
    "exec",
    "-e",
    "MYSQL_PWD=fuchang-it-mysql",
    mysqlContainer,
    "mysql",
    "-ufuchang",
    "-N",
    "-B",
    "fuchang_ticketing_it",
    "-e",
    `SELECT id,username FROM user WHERE username LIKE '${userPrefix}%' ORDER BY id`,
  ],
  { encoding: "utf8" },
)
  .trim()
  .split(/\r?\n/)
  .filter(Boolean)
  .map((line) => {
    const [id, username] = line.split("\t");
    return { userID: Number(id), username, token: mintToken(Number(id), jwtSecret) };
  });

if (rows.length !== userCount) {
  throw new Error(`expected ${userCount} fast users, got ${rows.length}`);
}

await mkdir(dirname(outPath), { recursive: true });
await writeFile(
  outPath,
  `${JSON.stringify(
    {
      prepared_at: new Date().toISOString(),
      base_url: process.env.BASE_URL || "http://127.0.0.1:18080/api/v1",
      campaign_id: campaign.campaignID,
      event_id: campaign.eventID,
      tier_id: campaign.tierID,
      total_quota: totalQuota,
      per_user_limit: perUserLimit,
      users: rows,
    },
    null,
    2,
  )}\n`,
  "utf8",
);
console.log(JSON.stringify({ fixture: outPath, campaign_id: campaign.campaignID, users: rows.length }, null, 2));

function mintToken(userID, secret) {
  const header = base64url(JSON.stringify({ alg: "HS256", typ: "JWT" }));
  const payload = base64url(
    JSON.stringify({
      user_id: userID,
      token_type: "access",
      exp: Math.floor(Date.now() / 1000) + 3600,
      iss: "zyh",
    }),
  );
  const unsigned = `${header}.${payload}`;
  const signature = createHmac("sha256", secret).update(unsigned).digest("base64url");
  return `${unsigned}.${signature}`;
}

function base64url(value) {
  return Buffer.from(value).toString("base64url");
}
