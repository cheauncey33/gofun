import { performance } from "node:perf_hooks";

const baseUrl = process.env.BASE_URL || "http://127.0.0.1:8080/api/v1";
const username = process.env.LOAD_USERNAME || "admin";
const password = process.env.LOAD_PASSWORD || "admin123";
const durationSeconds = Number(process.env.DURATION_SECONDS || 30);
const concurrency = Number(process.env.CONCURRENCY || 20);
const thinkMs = Number(process.env.THINK_MS || 0);
const path = process.env.PATH_TO_TEST || "/products?page=1&page_size=10";

const stats = {
  total: 0,
  ok: 0,
  failed: 0,
  statuses: new Map(),
  durations: [],
};

function percentile(sorted, p) {
  if (sorted.length === 0) return 0;
  const index = Math.ceil((p / 100) * sorted.length) - 1;
  return sorted[Math.max(0, Math.min(index, sorted.length - 1))];
}

function addStatus(status) {
  stats.statuses.set(status, (stats.statuses.get(status) || 0) + 1);
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function login() {
  const res = await fetch(`${baseUrl}/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  const body = await res.json();
  if (!res.ok || !body.data?.token) {
    throw new Error(`login failed: status=${res.status} body=${JSON.stringify(body)}`);
  }
  return body.data.token;
}

async function worker(id, token, deadline) {
  while (performance.now() < deadline) {
    const started = performance.now();
    try {
      const res = await fetch(`${baseUrl}${path}`, {
        headers: { Authorization: token },
      });
      await res.arrayBuffer();
      const elapsed = performance.now() - started;
      stats.total += 1;
      stats.durations.push(elapsed);
      addStatus(res.status);
      if (res.ok) stats.ok += 1;
      else stats.failed += 1;
    } catch (err) {
      const elapsed = performance.now() - started;
      stats.total += 1;
      stats.failed += 1;
      stats.durations.push(elapsed);
      addStatus("network_error");
      if (id === 0) console.error(err.message);
    }
    if (thinkMs > 0) {
      await sleep(thinkMs);
    }
  }
}

const token = await login();
const started = performance.now();
const deadline = started + durationSeconds * 1000;

await Promise.all(Array.from({ length: concurrency }, (_, i) => worker(i, token, deadline)));

const elapsedSeconds = (performance.now() - started) / 1000;
const sorted = stats.durations.slice().sort((a, b) => a - b);
const sum = stats.durations.reduce((acc, n) => acc + n, 0);
const average = sorted.length ? sum / sorted.length : 0;
const statuses = Object.fromEntries(stats.statuses.entries());

console.log(JSON.stringify({
  baseUrl,
  path,
  duration_seconds: Number(elapsedSeconds.toFixed(2)),
  concurrency,
  think_ms: thinkMs,
  total_requests: stats.total,
  requests_per_second: Number((stats.total / elapsedSeconds).toFixed(2)),
  ok: stats.ok,
  failed: stats.failed,
  failure_rate: Number((stats.failed / Math.max(stats.total, 1)).toFixed(4)),
  latency_ms: {
    avg: Number(average.toFixed(2)),
    p50: Number(percentile(sorted, 50).toFixed(2)),
    p90: Number(percentile(sorted, 90).toFixed(2)),
    p95: Number(percentile(sorted, 95).toFixed(2)),
    p99: Number(percentile(sorted, 99).toFixed(2)),
    max: Number((sorted.at(-1) || 0).toFixed(2)),
  },
  statuses,
}, null, 2));
