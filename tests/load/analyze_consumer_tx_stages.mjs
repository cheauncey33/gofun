#!/usr/bin/env node
/**
 * Estimate P50/P95/P99 from Prometheus histogram samples in lifecycle-samples.json.
 * Usage: node tests/load/analyze_consumer_tx_stages.mjs <lifecycle-samples.json>
 */
import fs from "node:fs";

const path = process.argv[2];
if (!path) {
  console.error("usage: node analyze_consumer_tx_stages.mjs <lifecycle-samples.json>");
  process.exit(1);
}

const raw = fs.readFileSync(path, "utf8").replace(/^\uFEFF/, "");
const samples = JSON.parse(raw);
const last = samples[samples.length - 1];
const prom = last.prometheus || {};

function parseLabels(raw) {
  const out = {};
  if (!raw) return out;
  for (const part of raw.split(",")) {
    const m = part.match(/^([^=]+)="(.*)"$/);
    if (m) out[m[1]] = m[2];
  }
  return out;
}

function collectHistograms(metricBase) {
  /** @type {Map<string, {buckets: Array<{le:number,c:number}>, count:number, sum:number}>} */
  const byKey = new Map();
  for (const [key, value] of Object.entries(prom)) {
    const m = key.match(
      new RegExp(`^${metricBase}_(bucket|sum|count)(?:\\{(.*)\\})?$`),
    );
    if (!m) continue;
    const kind = m[1];
    const labels = parseLabels(m[2] || "");
    const labelKey = Object.entries(labels)
      .filter(([k]) => k !== "le")
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([k, v]) => `${k}=${v}`)
      .join(",");
    if (!byKey.has(labelKey)) {
      byKey.set(labelKey, { buckets: [], count: 0, sum: 0 });
    }
    const h = byKey.get(labelKey);
    if (kind === "count") h.count = Number(value);
    else if (kind === "sum") h.sum = Number(value);
    else if (kind === "bucket") {
      const le = labels.le === "+Inf" ? Infinity : Number(labels.le);
      h.buckets.push({ le, c: Number(value) });
    }
  }
  for (const h of byKey.values()) {
    h.buckets.sort((a, b) => a.le - b.le);
  }
  return byKey;
}

function percentileMs(h, pct) {
  if (!h.count) return null;
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
  return {
    name,
    count: h.count,
    avg_ms: h.count ? Number(((h.sum / h.count) * 1000).toFixed(2)) : null,
    p50_ms: Number(percentileMs(h, 50).toFixed(2)),
    p95_ms: Number(percentileMs(h, 95).toFixed(2)),
    p99_ms: Number(percentileMs(h, 99).toFixed(2)),
    sum_s: Number(h.sum.toFixed(3)),
  };
}

const stageHist = collectHistograms("ticket_order_consumer_stage_duration_seconds");
const txHist = collectHistograms("ticket_order_consumer_transaction_duration_seconds");

const stages = [...stageHist.entries()]
  .map(([key, h]) => report(key || "stage", h))
  .sort((a, b) => b.avg_ms - a.avg_ms);

const tx =
  report(
    "whole_tx",
    txHist.get("result=success") ||
      [...txHist.values()][0] || { buckets: [], count: 0, sum: 0 },
  );

const stageAvgSum = stages.reduce((acc, row) => acc + (row.avg_ms || 0), 0);
const out = {
  source: path,
  last_timestamp: last.timestamp,
  last_phase: last.phase,
  whole_transaction: tx,
  stages,
  stage_avg_sum_ms: Number(stageAvgSum.toFixed(2)),
  residual_avg_ms: Number((tx.avg_ms - stageAvgSum).toFixed(2)),
  note:
    "residual_avg ≈ COMMIT + BEGIN + Go gap + uninstrumented SQL (e.g. tier remaining SELECT if folded into tier_bucket_update)",
};
console.log(JSON.stringify(out, null, 2));
