import { spawn, execFileSync } from "node:child_process";
import { open, mkdir, readFile, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { performance } from "node:perf_hooks";
import assert from "node:assert/strict";

const project = process.argv[2];
const output = resolve(process.argv[3]);
assert.match(project, /^gofun-scenarios-[a-z0-9-]+$/);
const base = "http://127.0.0.1:18580/api/v1";
const mysql = `${project}-mysql-1`, redis = `${project}-redis-1`, backend = `${project}-backend-load`;
const rabbit = `${project}-rabbitmq-1`;
const docker = (args, options = {}) => execFileSync("docker", args, { encoding: "utf8", ...options });
const sql = (query) => docker(["exec", "-e", "MYSQL_PWD=fuchang-it-mysql", mysql, "mysql", "-ufuchang", "-N", "-B", "fuchang_ticketing_it", "-e", query]).trim();
const json = async (path) => JSON.parse((await readFile(path, "utf8")).replace(/^\uFEFF/, ""));
const pause = (ms) => new Promise((done) => setTimeout(done, ms));
const mixedOnly = process.argv[4] === "mixed";
assert.ok(process.argv[4] === undefined || mixedOnly, "optional scenario must be mixed");
const results = mixedOnly ? (await json(join(output, "summary.json"))).filter((r) => r.name !== "mixed") : [];
await mkdir(output, { recursive: true });

async function profile(limits = {}) {
  const deadline = performance.now() + 90000;
  while (true) {
    try {
      docker(["exec", rabbit, "rabbitmq-diagnostics", "-q", "check_port_connectivity"], { stdio: "ignore" });
      break;
    } catch (_) {}
    assert.ok(performance.now() < deadline, "test broker did not become healthy");
    await pause(1000);
  }
  const environment = {
    ORDER_CONSUMER_WORKER_COUNT: "12", ORDER_OUTBOX_PUBLISH_WORKERS: "4",
    MYSQL_MAX_OPEN_CONNS: "100", MYSQL_WORKER_MAX_OPEN_CONNS: "0",
    DELAYED_ORDER_TIMEOUT_MINUTES: "60",
    RATELIMIT_GLOBAL_RATE: "100000", RATELIMIT_GLOBAL_BURST: "100000",
    RATELIMIT_IP_RATE: "100000", RATELIMIT_IP_BURST: "100000",
    RATELIMIT_WRITE_WINDOW_MS: "1000", RATELIMIT_WRITE_MAX_PER_WINDOW: "100000", ...limits,
  };
  await writeFile(join(output, "profile.json"), JSON.stringify({ services: { backend: { environment } } }));
  docker(["compose", "-p", project, "-f", "tests/integration/docker-compose.ticketing.yml", "-f", "tests/load/docker-compose.capacity.yml", "-f", "tests/load/docker-compose.audit.yml", "-f", join(output, "profile.json"), "up", "-d", "--no-deps", "--wait", "backend"], {
    env: { ...process.env, CAPACITY_CONTAINER_PREFIX: project, GOFUN_BACKEND_PORT: "18580" },
  });
  const readyDeadline = performance.now() + 90000;
  while (performance.now() < readyDeadline) {
    try {
      const response = await fetch("http://127.0.0.1:18580/healthz", { signal: AbortSignal.timeout(1000) });
      if (response.ok) return;
    } catch (_) {}
    await pause(500);
  }
  throw new Error("test backend health check timed out");
}

async function prepare(name, quota, count = 3000) {
  const dir = join(output, name);
  await mkdir(dir, { recursive: true });
  execFileSync(process.execPath, ["tests/load/k6/prepare_rush_fixture_fast.mjs"], {
    encoding: "utf8", env: { ...process.env, BASE_URL: base, MYSQL_CONTAINER: mysql,
      K6_FIXTURE: join(dir, "fixture.json"), K6_USERS: String(count), RUSH_TOTAL_QUOTA: String(quota),
      RUSH_LABEL: `scenario-${name}`, K6_JWT_SECRET: "fuchang-integration-test-secret-only" },
  });
  return { name, dir, fixture: await json(join(dir, "fixture.json")) };
}

async function runK6(test, mode, parameters = {}, script = "business_scenarios.js") {
  const env = { MODE: mode, RATE: "0", ITERATIONS: "1000", DURATION: "10s", GROUPS: "100", ...parameters,
    K6_FIXTURE: `/results/${test.name}/fixture.json`, K6_SUMMARY: `/results/${test.name}/k6-summary.json`,
    BASE_URL: "http://backend:8080/api/v1", LOAD_PASSWORD: "12345678" };
  const args = ["run", "--rm", "--network", `${project}_default`, "-v", `${resolve("tests/load/k6")}:/scripts:ro`, "-v", `${output}:/results`, "-w", "/scripts"];
  for (const [key, value] of Object.entries(env)) args.push("-e", `${key}=${value}`);
  args.push("grafana/k6:0.57.0", "run", script);
  const stdout = await open(join(test.dir, "k6.stdout.log"), "w"), stderr = await open(join(test.dir, "k6.stderr.log"), "w");
  const samples = [];
  const started = performance.now();
  const sample = async () => {
    try {
      const response = await fetch("http://127.0.0.1:18580/metrics", { signal: AbortSignal.timeout(1000) });
      const text = await response.text();
      const metrics = {};
      for (const line of text.split("\n")) {
        const match = line.match(/^([^ #]+) ([0-9.eE+-]+)$/);
        if (match) metrics[match[1]] = Number(match[2]);
      }
      samples.push({ elapsed_ms: performance.now() - started, metrics });
    } catch (error) { samples.push({ elapsed_ms: performance.now() - started, error: error.message }); }
  };
  await sample();
  const timer = setInterval(sample, 1000);
  let exitCode;
  try {
    exitCode = await new Promise((done, reject) => {
      const child = spawn("docker", args, { stdio: ["ignore", stdout.fd, stderr.fd] });
      child.once("error", reject);
      child.once("exit", done);
    });
  } finally {
    clearInterval(timer);
    await sample();
    await stdout.close(); await stderr.close();
    await writeFile(join(test.dir, "samples.json"), JSON.stringify(samples, null, 2));
  }
  const summary = await json(join(test.dir, "k6-summary.json"));
  test.load = { exit_code: exitCode, elapsed_ms: performance.now() - started, ...summary };
  return exitCode;
}

function orderCounts(test) {
  const tier = test.fixture.tier_id;
  return sql(`SELECT COUNT(*),COALESCE(SUM(o.status='queued'),0),COALESCE(SUM(o.status='pending_payment'),0),COALESCE(SUM(o.status='failed'),0) FROM ticket_order o JOIN ticket_order_item i ON i.order_id=o.id WHERE i.ticket_tier_id=${tier} AND o.delete_time IS NULL;`).split("\t").map(Number);
}

async function drain(test) {
  const started = performance.now();
  while (performance.now() - started < 180000) {
    const counts = orderCounts(test);
    const unfinished = Number(sql(`SELECT COUNT(*) FROM ticket_order_outbox b JOIN ticket_order_item i ON i.order_id=b.order_id WHERE i.ticket_tier_id=${test.fixture.tier_id} AND b.status IN ('pending','publishing');`));
    if (counts[1] === 0 && unfinished === 0) return (performance.now() - started) / 1000;
    await pause(1000);
  }
  throw new Error(`${test.name}: recovery did not finish within 180 seconds`);
}

function verify(test, expectedOrders) {
  const { campaign_id: campaign, tier_id: tier, total_quota: quota } = test.fixture;
  const [orders, queued, confirmed, failed] = orderCounts(test);
  const rushOrders = Number(sql(`SELECT COUNT(*) FROM ticket_order WHERE rush_sale_campaign_id=${campaign} AND delete_time IS NULL;`));
  const buckets = (table, where) => sql(`SELECT bucket_no,remaining_quota FROM ${table} WHERE ${where} ORDER BY bucket_no;`).split(/\r?\n/).map((row) => row.split("\t").map(Number));
  const rush = buckets("rush_campaign_bucket", `campaign_id=${campaign}`), normal = buckets("ticket_tier_bucket", `tier_id=${tier}`);
  const stock = (keys) => docker(["exec", "-e", "REDISCLI_AUTH=fuchang-it-redis", redis, "redis-cli", "--raw", "MGET", ...keys]).trim().split(/\r?\n/).map(Number);
  const rushRedis = stock(rush.map(([b]) => `fuchang:rush:stock:${campaign}:${b}`));
  const tierRedis = stock(normal.map(([b]) => `fuchang:ticket:stock:${tier}:${b}`));
  const remaining = (rows) => rows.reduce((sum, [, count]) => sum + count, 0);
  const inbox = Number(sql(`SELECT COUNT(*) FROM ticket_order_consumer_inbox b JOIN ticket_order_item i ON i.order_id=b.order_id WHERE i.ticket_tier_id=${tier};`));
  const outboxFailed = Number(sql(`SELECT COUNT(*) FROM ticket_order_outbox b JOIN ticket_order_item i ON i.order_id=b.order_id WHERE i.ticket_tier_id=${tier} AND b.status='failed';`));
  const maxPurchased = Number(sql(`SELECT COALESCE(MAX(qty),0) FROM (SELECT SUM(i.quantity) qty FROM ticket_order o JOIN ticket_order_item i ON i.order_id=o.id WHERE o.rush_sale_campaign_id=${campaign} AND o.delete_time IS NULL GROUP BY o.user_id) purchases;`));
  const redisMatches = rush.every(([, count], n) => count === rushRedis[n]) && normal.every(([, count], n) => count === tierRedis[n]);
  const passed = (expectedOrders === undefined || orders === expectedOrders) && queued === 0 && failed === 0 && confirmed === orders
    && inbox === orders && outboxFailed === 0 && redisMatches && remaining(rush) === quota - rushOrders
    && remaining(normal) === quota - orders && [...rush, ...normal].every(([, count]) => count >= 0) && maxPurchased <= test.fixture.per_user_limit;
  return { orders, confirmed, failed, queued, rush_orders: rushOrders, inbox, outbox_failed: outboxFailed,
    rush_remaining: remaining(rush), tier_remaining: remaining(normal), max_purchased_per_user: maxPurchased, redis_matches_db: redisMatches, passed };
}

async function save(test) {
  results.push({ name: test.name, load: test.load, verification: test.verification, extra: test.extra });
  await writeFile(join(test.dir, "result.json"), JSON.stringify(results.at(-1), null, 2));
  await writeFile(join(output, "summary.json"), JSON.stringify(results, null, 2));
  console.log(JSON.stringify({ name: test.name, passed: test.verification.passed, orders: test.verification.orders, load: test.load.outcomes || test.load.mixed_action_ok_rate }));
  assert.equal(test.verification.passed, true, `${test.name}: database/inventory verification failed`);
  assert.equal(test.load.exit_code, 0, `${test.name}: k6 threshold failed`);
  if (test.load.dropped_iterations !== undefined) assert.equal(test.load.dropped_iterations, 0, `${test.name}: load generator dropped iterations`);
}

sql("UPDATE ticket_order SET expires_at=DATE_ADD(expires_at, INTERVAL 1 HOUR) WHERE status='pending_payment';");
await profile();
if (!mixedOnly) {
const sold = await prepare("sold-out", 1000);
await runK6(sold, "sold_out", { ITERATIONS: "5000" });
await drain(sold);
sold.verification = verify(sold, 1000);
sold.verification.passed &&= sold.load.outcomes.sold_out > 0 && sold.load.outcomes.rate_limited === 0;
await save(sold);

const duplicate = await prepare("duplicate", 10000);
await runK6(duplicate, "duplicate", { ITERATIONS: "1000" });
await drain(duplicate);
duplicate.verification = verify(duplicate, 100);
let conflicts = 0;
for (let i = 0; i < 100; i++) {
  const response = await fetch(`${base}/rush-sales/${duplicate.fixture.campaign_id}/execute`, {
    method: "POST", headers: { Authorization: duplicate.fixture.users[i].token, "Content-Type": "application/json", "X-Idempotency-Key": `duplicate-${duplicate.fixture.campaign_id}-${i}` },
    body: JSON.stringify({ quantity: 2, contact_name: "场景压测", contact_phone: "13900139000", terms_accepted: true, attendees: [] }),
  });
  if (response.status === 400 && (await response.json()).code === 40000) conflicts++;
}
duplicate.extra = { changed_payload_rejected: conflicts, probes: 100 };
duplicate.verification.passed &&= conflicts === 100 && orderCounts(duplicate)[0] === 100;
await save(duplicate);

const purchase = await prepare("purchase-limit", 1000);
await runK6(purchase, "purchase_limit", { ITERATIONS: "200" });
await drain(purchase);
purchase.verification = verify(purchase, 20);
await save(purchase);

for (const [name, limits, upper] of [
  ["limit-global", { RATELIMIT_GLOBAL_RATE: "50", RATELIMIT_GLOBAL_BURST: "50" }, 325],
  ["limit-ip", { RATELIMIT_IP_RATE: "50", RATELIMIT_IP_BURST: "50" }, 325],
  ["limit-write", { RATELIMIT_WRITE_MAX_PER_WINDOW: "10" }, 60],
]) {
  await profile();
  const test = await prepare(name, 1000);
  await profile(limits);
  await runK6(test, "rate_limit", { RATE: "200", DURATION: "5s" });
  await drain(test);
  test.verification = verify(test, 1);
  test.verification.passed &&= test.load.outcomes.rate_limited > 0 && test.load.outcomes.accepted > 0 && test.load.outcomes.accepted <= upper;
  test.extra = { limits, accepted_upper_bound_with_tolerance: upper };
  await save(test);
}

await profile();
const fault = await prepare("mq-outage", 10000);
docker(["stop", rabbit]);
try {
  await runK6(fault, "fault", { RATE: "100", DURATION: "10s" });
  fault.extra = { while_broker_down: orderCounts(fault) };
  assert.equal(fault.extra.while_broker_down[1], fault.extra.while_broker_down[0], "orders unexpectedly confirmed while broker is stopped");
} finally { docker(["start", rabbit]); }
fault.extra.recovery_seconds = await drain(fault);
fault.verification = verify(fault, fault.load.outcomes.accepted);
await save(fault);

const restart = await prepare("restart-after-accept", 10000);
docker(["stop", rabbit]);
try {
  await runK6(restart, "fault", { RATE: "100", DURATION: "5s" });
  restart.extra = { before_restart: orderCounts(restart) };
  assert.equal(restart.extra.before_restart[1], restart.extra.before_restart[0], "restart fixture must contain unconfirmed orders");
  docker(["kill", backend]);
} finally { docker(["start", rabbit]); }
await profile();
restart.extra.recovery_seconds = await drain(restart);
restart.verification = verify(restart, restart.load.outcomes.accepted);
await save(restart);
}

await profile();
const mixed = await prepare("mixed", 100000, 200);
const ids = mixed.fixture.users.map((user) => user.userID).join(",");
const ownerPassword = sql(`SELECT u.password FROM user u JOIN organizer_member m ON m.user_id=u.id JOIN event e ON e.organizer_id=m.organizer_id JOIN event_session s ON s.event_id=e.id JOIN ticket_tier t ON t.session_id=s.id WHERE t.id=${mixed.fixture.tier_id} AND m.role='owner' LIMIT 1;`);
sql(`UPDATE user SET password='${ownerPassword}' WHERE id IN (${ids});`);
for (const user of mixed.fixture.users) {
  const response = await fetch(`${base}/rush-sales/${mixed.fixture.campaign_id}/execute`, {
    method: "POST", headers: { Authorization: user.token, "Content-Type": "application/json", "X-Idempotency-Key": `mixed-seed-${mixed.fixture.campaign_id}-${user.userID}` },
    body: JSON.stringify({ quantity: 1, contact_name: "混合预置", contact_phone: "13900139000", terms_accepted: true, attendees: [] }),
  });
  assert.equal(response.status, 200, "mixed order fixture preparation failed");
}
await drain(mixed);
await runK6(mixed, "mixed", { RATE: "100", DURATION: "60s" }, "mixed_traffic.js");
await drain(mixed);
mixed.verification = verify(mixed);
await save(mixed);

await writeFile(join(output, "report.md"), [
  "# 业务场景压测", "",
  "后端 2 CPU / 1GB，MySQL 2 CPU / 2GB，32 库存桶、12 Consumer、4 Publisher、共享 100 连接，redo/binlog 双 1。每场景独立活动。",
  "",
  "| 场景 | 请求数 | 已确认订单 | Redis/DB 库存一致 | 验收 |",
  "|---|---:|---:|---|---|",
  ...results.map((r) => `| ${r.name} | ${r.load.http_requests || r.load.http_reqs} | ${r.verification.orders} | ${r.verification.redis_matches_db} | ${r.verification.passed && r.load.exit_code === 0 ? "通过" : "失败"} |`),
  "",
  "| 场景 | 放行响应 | 售罄 | 业务拒绝 | 限流 | 放行 P99 ms | 拒绝 P99 ms |",
  "|---|---:|---:|---:|---:|---:|---:|",
  ...results.filter((r) => r.load.outcomes).map(({ name, load: l }) => `| ${name} | ${l.outcomes.accepted} | ${l.outcomes.sold_out} | ${l.outcomes.business_rejected} | ${l.outcomes.rate_limited} | ${l.accepted_p99_ms.toFixed(2)} | ${l.rejected_p99_ms.toFixed(2)} |`),
  "",
  "| 混合动作 | 次数 | 成功率 | P95 ms | P99 ms |",
  "|---|---:|---:|---:|---:|",
  ...Object.entries(mixed.load.action_counts).map(([action, count]) => `| ${action} | ${count} | ${(mixed.load.action_success_rate[action] * 100).toFixed(2)}% | ${mixed.load.action_latency[action].p95_ms.toFixed(2)} | ${mixed.load.action_latency[action].p99_ms.toFixed(2)} |`),
  "",
  "售罄、限购、限流分别统计，拒绝 QPS 不代表下单吞吐。限流单项测试提高其他层阈值，global/IP 配额 50/s、burst 50，Redis 用户滑窗 10/s，输入 200/s 持续 5 秒。",
  "MQ 故障通过停止独立测试 Broker 注入；重启场景在已受理未确认时强制终止应用，然后恢复 Broker 与应用。混合测试 100 个业务动作/秒持续 60 秒，动作可能包含多个 HTTP 请求；登录使用真实 bcrypt 密码，订单详情用户预置了订单。",
  "MQ recovery_seconds 从 Docker start 返回后计时；应用重启 recovery_seconds 从应用健康检查通过后计时，不包含应用启动时间。",
  "这些短时测试不覆盖长时间运行、支付与超时竞争的负载测试、Redis 丢数据或多应用实例故障。原始分场景统计、限流配置、恢复耗时与库存核对见 summary.json 和各场景目录。",
].join("\n") + "\n");
console.log(`Report: ${join(output, "report.md")}`);
