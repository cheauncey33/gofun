import { performance } from "node:perf_hooks";

export const cfg = {
  baseUrl: process.env.BASE_URL || "http://127.0.0.1:8080/api/v1",
  adminUsername: process.env.ADMIN_USERNAME || "admin",
  adminPassword: process.env.ADMIN_PASSWORD || "admin123",
  loadUserPrefix: process.env.LOAD_USER_PREFIX || "load_user_",
  loadPassword: process.env.LOAD_PASSWORD || "123456",
  dormId: Number(process.env.DORM_ID || 1),
  durationSeconds: Number(process.env.DURATION_SECONDS || 60),
  concurrency: Number(process.env.CONCURRENCY || 50),
  thinkMs: Number(process.env.THINK_MS || 0),
};

export class Metrics {
  constructor(name) {
    this.name = name;
    this.startedAt = performance.now();
    this.total = 0;
    this.ok = 0;
    this.failed = 0;
    this.byLabel = new Map();
    this.byStatus = new Map();
    this.durations = [];
    this.errors = new Map();
  }

  record(label, status, ms, ok, error = "") {
    this.total += 1;
    if (ok) this.ok += 1;
    else this.failed += 1;
    this.durations.push(ms);
    this.byLabel.set(label, (this.byLabel.get(label) || 0) + 1);
    this.byStatus.set(String(status), (this.byStatus.get(String(status)) || 0) + 1);
    if (error) this.errors.set(error, (this.errors.get(error) || 0) + 1);
  }

  summary(extra = {}) {
    const elapsedSeconds = (performance.now() - this.startedAt) / 1000;
    const sorted = this.durations.slice().sort((a, b) => a - b);
    const sum = sorted.reduce((acc, n) => acc + n, 0);
    return {
      scenario: this.name,
      baseUrl: cfg.baseUrl,
      duration_seconds: Number(elapsedSeconds.toFixed(2)),
      concurrency: cfg.concurrency,
      think_ms: cfg.thinkMs,
      total_requests: this.total,
      requests_per_second: Number((this.total / Math.max(elapsedSeconds, 0.001)).toFixed(2)),
      ok: this.ok,
      failed: this.failed,
      failure_rate: Number((this.failed / Math.max(this.total, 1)).toFixed(4)),
      latency_ms: {
        avg: Number((sum / Math.max(sorted.length, 1)).toFixed(2)),
        p50: round(percentile(sorted, 50)),
        p90: round(percentile(sorted, 90)),
        p95: round(percentile(sorted, 95)),
        p99: round(percentile(sorted, 99)),
        max: round(sorted.at(-1) || 0),
      },
      labels: Object.fromEntries(this.byLabel.entries()),
      statuses: Object.fromEntries(this.byStatus.entries()),
      top_errors: Object.fromEntries([...this.errors.entries()].slice(0, 10)),
      ...extra,
    };
  }
}

export function round(n) {
  return Number(n.toFixed(2));
}

export function percentile(sorted, p) {
  if (sorted.length === 0) return 0;
  const index = Math.ceil((p / 100) * sorted.length) - 1;
  return sorted[Math.max(0, Math.min(index, sorted.length - 1))];
}

export function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export function randomInt(min, max) {
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

export function pickWeighted(items) {
  const total = items.reduce((sum, item) => sum + item.weight, 0);
  let cursor = Math.random() * total;
  for (const item of items) {
    cursor -= item.weight;
    if (cursor <= 0) return item;
  }
  return items.at(-1);
}

export async function httpJson(method, path, { token, body, label, metrics, okStatuses = [200] } = {}) {
  const started = performance.now();
  let status = "network_error";
  try {
    const headers = { "Content-Type": "application/json" };
    if (token) headers.Authorization = token;
    const res = await fetch(`${cfg.baseUrl}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    status = res.status;
    const text = await res.text();
    const data = text ? JSON.parse(text) : null;
    const ok = okStatuses.includes(res.status);
    metrics?.record(label || `${method} ${path}`, status, performance.now() - started, ok, ok ? "" : data?.msg || text);
    return { res, data, ok };
  } catch (err) {
    metrics?.record(label || `${method} ${path}`, status, performance.now() - started, false, err.message);
    return { res: null, data: null, ok: false, error: err };
  }
}

export async function login(username, password, metrics) {
  const { data, ok } = await httpJson("POST", "/login", {
    body: { username, password },
    label: "login",
    metrics,
  });
  if (!ok || !data?.data?.token) {
    throw new Error(`login failed for ${username}`);
  }
  return data.data.token;
}

export async function ensureUser(username, password = cfg.loadPassword, metrics) {
  const probe = await httpJson("POST", "/login", {
    body: { username, password },
    label: "login_probe",
    metrics,
    okStatuses: [200, 401],
  });
  if (probe.data?.data?.token) {
    return probe.data.data.token;
  }

  try {
    await httpJson("POST", "/register", {
      body: { username, password, dorm_id: cfg.dormId },
      label: "register",
      metrics,
      okStatuses: [200, 400],
    });
    return login(username, password, metrics);
  } catch (err) {
    throw new Error(`ensure user failed for ${username}: ${err.message}`);
  }
}

export async function getProducts(token, metrics, pageSize = 20) {
  const { data, ok } = await httpJson("GET", `/products?page=1&page_size=${pageSize}`, {
    token,
    label: "products_list",
    metrics,
  });
  if (!ok) return [];
  return data?.data?.list || [];
}

export function productID(product) {
  return Number(product.id);
}

export async function runWorkers(metrics, worker) {
  const deadline = performance.now() + cfg.durationSeconds * 1000;
  await Promise.all(Array.from({ length: cfg.concurrency }, (_, index) => worker(index, deadline)));
  console.log(JSON.stringify(metrics.summary(), null, 2));
}
