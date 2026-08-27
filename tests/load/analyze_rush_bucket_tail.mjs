#!/usr/bin/env node
import fs from "node:fs";

function loadJson(p) {
  return JSON.parse(fs.readFileSync(p, "utf8").replace(/^\uFEFF/, ""));
}

function parseLabels(raw) {
  const out = {};
  if (!raw) return out;
  for (const part of raw.split(",")) {
    const m = part.match(/^([^=]+)="(.*)"$/);
    if (m) out[m[1]] = m[2];
  }
  return out;
}

function collectHistograms(prom, metricBase) {
  const byKey = new Map();
  for (const [key, value] of Object.entries(prom || {})) {
    const m = key.match(new RegExp(`^${metricBase}_(bucket|sum|count)(?:\\{(.*)\\})?$`));
    if (!m) continue;
    const kind = m[1];
    const labels = parseLabels(m[2] || "");
    const labelKey = Object.entries(labels)
      .filter(([k]) => k !== "le")
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([k, v]) => `${k}=${v}`)
      .join(",");
    if (!byKey.has(labelKey)) byKey.set(labelKey, { buckets: [], count: 0, sum: 0 });
    const h = byKey.get(labelKey);
    if (kind === "count") h.count = Number(value);
    else if (kind === "sum") h.sum = Number(value);
    else {
      const le = labels.le === "+Inf" ? Infinity : Number(labels.le);
      h.buckets.push({ le, c: Number(value) });
    }
  }
  for (const h of byKey.values()) h.buckets.sort((a, b) => a.le - b.le);
  return byKey;
}

function percentileMs(h, pct) {
  if (!h?.count) return null;
  const target = h.count * (pct / 100);
  let prevLe = 0;
  let prevC = 0;
  for (const b of h.buckets) {
    if (b.c >= target) {
      if (!Number.isFinite(b.le)) return prevLe * 1000;
      if (b.c === prevC) return b.le * 1000;
      const frac = (target - prevC) / (b.c - prevC);
      return (prevLe + frac * (b.le - prevLe)) * 1000;
    }
    prevLe = b.le;
    prevC = b.c;
  }
  return null;
}

function report(name, h) {
  if (!h?.count) return { name, count: 0 };
  return {
    name,
    count: h.count,
    avg_ms: Number(((h.sum / h.count) * 1000).toFixed(2)),
    p50_ms: Number(percentileMs(h, 50).toFixed(2)),
    p95_ms: Number(percentileMs(h, 95).toFixed(2)),
    p99_ms: Number(percentileMs(h, 99).toFixed(2)),
  };
}

const runs = [
  ["1+1", "tests/load/results/tx-profile-c6b32-20260810/vus-500/lifecycle-samples.json"],
  ["1+0", "tests/load/results/full-chain-1plus0-20260810/vus-500/lifecycle-samples.json"],
  ["nobinlog+redo1", "tests/load/results/full-chain-nobinlog-20260810/vus-500/lifecycle-samples.json"],
];

const out = {};
for (const [label, path] of runs) {
  const samples = loadJson(path);
  const last = samples[samples.length - 1];
  const prom = last.prometheus || {};
  const stages = collectHistograms(prom, "ticket_order_consumer_stage_duration_seconds");
  const tx = collectHistograms(prom, "ticket_order_consumer_transaction_duration_seconds");
  const inv = collectHistograms(prom, "ticket_order_consumer_inventory_bucket_duration_seconds");

  const rushStage = stages.get("stage=rush_bucket_update");
  const tierStage = stages.get("stage=tier_bucket_update");
  const commit = stages.get("stage=commit");
  const whole = tx.get("result=success");

  const rushBuckets = [...inv.entries()]
    .filter(([k]) => k.includes("kind=rush"))
    .map(([k, h]) => {
      const bn = (k.match(/bucket_no=([^,]+)/) || [])[1] || k;
      return { bucket_no: bn, ...report(`rush[${bn}]`, h) };
    })
    .sort((a, b) => b.p95_ms - a.p95_ms);

  const tierBuckets = [...inv.entries()]
    .filter(([k]) => k.includes("kind=tier"))
    .map(([k, h]) => {
      const bn = (k.match(/bucket_no=([^,]+)/) || [])[1] || k;
      return { bucket_no: bn, ...report(`tier[${bn}]`, h) };
    })
    .sort((a, b) => b.p95_ms - a.p95_ms);

  // lock waits from lifecycle samples
  let lockPeak = 0;
  let lockWaitsEnd = 0;
  let lockTimeEnd = 0;
  for (const s of samples) {
    const m = s.mysql_lock_waits || s.prometheus;
    // diagnostic samples put innodb in top-level during http; drain has mysql fields differently
  }
  const diagPath = path.replace("lifecycle-samples.json", "diagnostic-summary.json");
  const runPath = path.replace("lifecycle-samples.json", "run-summary.json");
  let lock_waits_delta = null;
  let lock_current_wait_peak = null;
  if (fs.existsSync(runPath)) {
    const run = loadJson(runPath);
    lock_waits_delta = run.lock_waits_delta ?? null;
    lock_current_wait_peak = run.lock_current_wait_peak ?? null;
  } else if (fs.existsSync(diagPath)) {
    const diag = loadJson(diagPath);
    lock_waits_delta = diag.peaks?.innodb_row_lock_waits_delta ?? null;
    lock_current_wait_peak = diag.peaks?.innodb_row_lock_current_waits ?? null;
  }

  const rushP95 = percentileMs(rushStage, 95);
  const commitP95 = percentileMs(commit, 95);
  const txP95 = percentileMs(whole, 95);

  out[label] = {
    whole: report("whole_tx", whole),
    commit: report("commit", commit),
    rush_stage: report("rush_bucket_update", rushStage),
    tier_stage: report("tier_bucket_update", tierStage),
    rush_vs_commit_p95_pct: commitP95 ? Number(((rushP95 / commitP95) * 100).toFixed(1)) : null,
    rush_vs_tx_p95_pct: txP95 ? Number(((rushP95 / txP95) * 100).toFixed(1)) : null,
    rush_vs_commit_p99_pct: Number(
      ((percentileMs(rushStage, 99) / percentileMs(commit, 99)) * 100).toFixed(1),
    ),
    lock_waits_delta,
    lock_current_wait_peak,
    rush_bucket_count: rushBuckets.length,
    hottest_rush_buckets: rushBuckets.slice(0, 8),
    coolest_rush_buckets: rushBuckets.slice(-5),
    hottest_tier_buckets: tierBuckets.slice(0, 5),
    rush_bucket_p95_max: rushBuckets[0]?.p95_ms ?? null,
    rush_bucket_p99_max: rushBuckets[0]?.p99_ms ?? null,
    rush_bucket_p95_median: rushBuckets.length
      ? rushBuckets[Math.floor(rushBuckets.length / 2)].p95_ms
      : null,
  };
}

const outPath = "tests/load/results/full-chain-nobinlog-20260810/rush-bucket-tail-analysis.json";
fs.writeFileSync(outPath, JSON.stringify(out, null, 2));
console.log(JSON.stringify(out, null, 2));
console.log("wrote", outPath);
