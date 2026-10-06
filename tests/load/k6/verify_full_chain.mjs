import { execFileSync } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { performance } from "node:perf_hooks";

const root = resolve(process.argv[2]);
const project = process.argv[3];
const base = process.argv[4];
const sql = (query) => execFileSync("docker", ["exec", "-e", "MYSQL_PWD=fuchang-it-mysql", `${project}-mysql-1`, "mysql", "-ufuchang", "-N", "-B", "fuchang_ticketing_it", "-e", query], { encoding: "utf8" }).trim();
const redis = (keys) => execFileSync("docker", ["exec", "-e", "REDISCLI_AUTH=fuchang-it-redis", `${project}-redis-1`, "redis-cli", "--raw", "MGET", ...keys], { encoding: "utf8" }).trim().split(/\r?\n/).map(Number);
const summary = JSON.parse((await readFile(join(root, "baseline-summary.json"), "utf8")).replace(/^\uFEFF/, ""));
const runs = Array.isArray(summary) ? summary : [summary];
const checks = [];
for (const run of runs) {
  const id = String(run.campaign_id);
  if (!/^\d+$/.test(id)) throw new Error("Invalid campaign ID");
  const counts = sql(`SELECT COUNT(*),SUM(status='pending_payment'),SUM(status='failed'),SUM(status='queued') FROM ticket_order WHERE rush_sale_campaign_id=${id} AND delete_time IS NULL;`).split("\t").map(Number);
  const inbox = Number(sql(`SELECT COUNT(*) FROM ticket_order_consumer_inbox i JOIN ticket_order o ON o.id=i.order_id WHERE o.rush_sale_campaign_id=${id};`));
  const tierId = sql(`SELECT ticket_tier_id FROM rush_sale_campaign WHERE id=${id};`);
  const bucketRows = sql(`SELECT bucket_no,remaining_quota FROM rush_campaign_bucket WHERE campaign_id=${id} ORDER BY bucket_no;`).split(/\r?\n/).map((line) => line.split("\t").map(Number));
  const tierRows = sql(`SELECT bucket_no,remaining_quota FROM ticket_tier_bucket WHERE tier_id=${tierId} ORDER BY bucket_no;`).split(/\r?\n/).map((line) => line.split("\t").map(Number));
  const rushStock = redis(bucketRows.map(([bucket]) => `fuchang:rush:stock:${id}:${bucket}`));
  const tierStock = redis(tierRows.map(([bucket]) => `fuchang:ticket:stock:${tierId}:${bucket}`));
  const quota = Number(sql(`SELECT total_quota FROM rush_sale_campaign WHERE id=${id};`));
  const remaining = bucketRows.reduce((sum, [, value]) => sum + value, 0);
  const exactStock = bucketRows.every(([, count], index) => count === rushStock[index]) && tierRows.every(([, count], index) => count === tierStock[index]);
  const passed = counts[0] === run.http_200_estimate && counts[1] === counts[0] && counts[2] === 0 && counts[3] === 0 && inbox === counts[0] && quota - remaining === counts[0] && exactStock;
  checks.push({ vus: run.vus, campaign_id: id, orders: counts[0], confirmed: counts[1], failed: counts[2], queued: counts[3], inbox, quota, remaining, redis_matches_db_buckets: exactStock, passed });
}
const lastId = String(runs.at(-1).campaign_id);
const [organizer, event, username] = sql(`SELECT e.organizer_id,e.id,u.username FROM rush_sale_campaign c JOIN ticket_tier t ON t.id=c.ticket_tier_id JOIN event_session s ON s.id=t.session_id JOIN event e ON e.id=s.event_id JOIN organizer_member m ON m.organizer_id=e.organizer_id AND m.role='owner' JOIN user u ON u.id=m.user_id WHERE c.id=${lastId};`).split("\t");
const login = await fetch(`${base}/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username, password: "12345678" }) });
const auth = await login.json();
const token = auth.data?.access_token || auth.data?.token;
if (!token) throw new Error("Fixture owner login failed");
const times = [];
let board;
for (let i = 0; i < 21; i++) {
  const started = performance.now();
  const response = await fetch(`${base}/organizers/${organizer}/funnel?event_id=${event}&days=7`, { headers: { Authorization: token } });
  board = await response.json();
  times.push(performance.now() - started);
  if (!response.ok || !board.data) throw new Error(`Dashboard query failed: ${response.status}`);
}
const submitted = board.data.steps.find((step) => step.key === "submitted")?.count;
const dashboard = { requests: times.length, first_request_ms: times[0], warm_average_ms: times.slice(1).reduce((a, b) => a + b) / 20, submitted, expected_submitted: runs.at(-1).http_200_estimate, passed: submitted === runs.at(-1).http_200_estimate };
const result = { measured_at: new Date().toISOString(), checks, dashboard, passed: checks.every((row) => row.passed) && dashboard.passed };
await writeFile(join(root, "verification.json"), JSON.stringify(result, null, 2) + "\n");
console.log(JSON.stringify(result, null, 2));
if (!result.passed) process.exitCode = 1;
