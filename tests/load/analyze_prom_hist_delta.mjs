import fs from "node:fs";

function loadPromMap(path) {
  const text = fs.readFileSync(path, "utf8").replace(/^\uFEFF/, "");
  if (path.endsWith(".json")) {
    return JSON.parse(text);
  }
  const metrics = {};
  for (const line of text.split(/\r?\n/)) {
    if (!line || line.startsWith("#")) continue;
    const m = line.match(
      /^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{([^}]*)\})?\s+([-+0-9.eE]+)$/,
    );
    if (!m) continue;
    const key = m[2] ? `${m[1]}{${m[2]}}` : m[1];
    metrics[key] = Number(m[3]);
  }
  return metrics;
}

function labelMap(labelStr) {
  const out = {};
  for (const part of labelStr.split(",")) {
    const m = part.trim().match(/^([a-zA-Z_][a-zA-Z0-9_]*)="(.*)"$/);
    if (m) out[m[1]] = m[2];
  }
  return out;
}

function histDelta(before, after, metricBase, requiredLabels = {}) {
  const buckets = [];
  let count = 0;
  let sum = 0;
  for (const [key, value] of Object.entries(after)) {
    if (!key.startsWith(metricBase + "_") && key !== metricBase) continue;
    if (
      !key.startsWith(metricBase + "_bucket") &&
      !key.startsWith(metricBase + "_count") &&
      !key.startsWith(metricBase + "_sum")
    ) {
      continue;
    }
    const labelStart = key.indexOf("{");
    const labels = labelStart >= 0 ? labelMap(key.slice(labelStart + 1, -1)) : {};
    let ok = true;
    for (const [k, v] of Object.entries(requiredLabels)) {
      if (labels[k] !== v) {
        ok = false;
        break;
      }
    }
    if (!ok) continue;
    const prev = before[key] || 0;
    const delta = value - prev;
    if (key.includes("_count{") || key.endsWith("_count")) count = delta;
    else if (key.includes("_sum{") || key.endsWith("_sum")) sum = delta;
    else if (key.includes("_bucket{") && labels.le != null) {
      const le = labels.le === "+Inf" ? Infinity : Number(labels.le);
      buckets.push({ le, c: delta });
    }
  }
  buckets.sort((a, b) => a.le - b.le);
  return { count, sum, buckets };
}

function percentileMs(hist, p) {
  if (!hist || hist.count <= 0 || !hist.buckets.length) return null;
  const target = hist.count * (p / 100);
  let prevLe = 0;
  let prevC = 0;
  for (const b of hist.buckets) {
    if (b.c >= target) {
      if (!Number.isFinite(b.le)) return prevLe * 1000;
      if (b.c === prevC) return b.le * 1000;
      const frac = (target - prevC) / (b.c - prevC);
      return (prevLe + frac * (b.le - prevLe)) * 1000;
    }
    if (Number.isFinite(b.le)) prevLe = b.le;
    prevC = b.c;
  }
  return prevLe * 1000;
}

function summarize(hist) {
  if (!hist || hist.count <= 0) {
    return { count: 0, avg_ms: null, p50_ms: null, p95_ms: null, p99_ms: null };
  }
  return {
    count: Math.round(hist.count),
    avg_ms: Number(((hist.sum / hist.count) * 1000).toFixed(3)),
    p50_ms: Number(percentileMs(hist, 50)?.toFixed(3)),
    p95_ms: Number(percentileMs(hist, 95)?.toFixed(3)),
    p99_ms: Number(percentileMs(hist, 99)?.toFixed(3)),
  };
}

const before = loadPromMap(process.argv[2]);
const after = loadPromMap(process.argv[3]);
const outPath = process.argv[4];

const report = {
  tx_success: summarize(
    histDelta(before, after, "ticket_order_consumer_transaction_duration_seconds", {
      result: "success",
    }),
  ),
  commit: summarize(
    histDelta(before, after, "ticket_order_consumer_stage_duration_seconds", {
      stage: "commit",
    }),
  ),
  order_lock: summarize(
    histDelta(before, after, "ticket_order_consumer_stage_duration_seconds", {
      stage: "order_lock",
    }),
  ),
  rush_bucket_update: summarize(
    histDelta(before, after, "ticket_order_consumer_stage_duration_seconds", {
      stage: "rush_bucket_update",
    }),
  ),
  tier_bucket_update: summarize(
    histDelta(before, after, "ticket_order_consumer_stage_duration_seconds", {
      stage: "tier_bucket_update",
    }),
  ),
  order_state_update: summarize(
    histDelta(before, after, "ticket_order_consumer_stage_duration_seconds", {
      stage: "order_state_update",
    }),
  ),
};

fs.writeFileSync(outPath, JSON.stringify(report, null, 2));
console.log(JSON.stringify(report, null, 2));
