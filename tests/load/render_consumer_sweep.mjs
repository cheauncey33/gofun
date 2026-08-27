#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";

const outDir = process.argv[2];
if (!outDir) {
  console.error("usage: node render_consumer_sweep.mjs <output-dir>");
  process.exit(1);
}

function loadJson(p) {
  return JSON.parse(fs.readFileSync(p, "utf8").replace(/^\uFEFF/, ""));
}

const summary = loadJson(path.join(outDir, "sweep-summary.json"));
const rows = (Array.isArray(summary) ? summary : [summary]).slice().sort(
  (a, b) => a.consumer_workers - b.consumer_workers,
);

function stageOf(runDir) {
  const p = path.join(outDir, runDir, "stage-percentiles.json");
  if (!fs.existsSync(p)) return null;
  return loadJson(p);
}

const enriched = rows.map((r) => {
  const stages = stageOf(`consumer-${r.consumer_workers}`);
  const byName = Object.fromEntries(
    (stages?.stages || []).map((s) => [s.name.replace(/^stage=/, ""), s]),
  );
  return {
    ...r,
    tx: stages?.whole_transaction || null,
    commit: byName.commit || null,
    rush: byName.rush_bucket_update || null,
  };
});

function throughputOf(r) {
  // Prefer busy-window consumer transaction rate; legacy orders/drain inflates
  // when consumers keep up during the HTTP window and drain waits on outbox churn.
  if (Number(r.consumer_tx_per_sec) > 0) return Number(r.consumer_tx_per_sec);
  return Number(r.orders_per_sec_drain) || 0;
}

let md = "";
md += "# Consumer sweep (nobinlog + redo=1)\n\n";
md += "固定：bucket=32, prefetch=5, outbox publishers=4, VUS=500, Duration=20s。\n\n";
md += "每轮在消费速率平稳后截断并 purge 剩余 MQ，不等待队列排空。主指标 `tx/s` 优先取稳定窗口瞬时速率均值。\n\n";
md += "| Consumer | tx/s | orders/drain | orders | busy_s | drain_s | HTTP req/s | tx P50 | tx P95 | commit P50 | commit P95 | rush P95 | rush P99 | lockΔ | dead |\n";
md += "| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n";
for (const r of enriched) {
  md += `| ${r.consumer_workers} | ${throughputOf(r)} | ${r.orders_per_sec_drain} | ${r.orders_after} | ${r.busy_seconds ?? "-"} | ${r.drain_seconds} | ${Number(r.http_req_rate).toFixed(1)} | ${r.tx?.p50_ms ?? "-"} | ${r.tx?.p95_ms ?? "-"} | ${r.commit?.p50_ms ?? "-"} | ${r.commit?.p95_ms ?? "-"} | ${r.rush?.p95_ms ?? "-"} | ${r.rush?.p99_ms ?? "-"} | ${r.lock_waits_delta} | ${r.dead_letters_after} |\n`;
}

// Inflection: max tx/s, and smallest C reaching >=95% of peak.
let best = enriched[0];
for (const r of enriched) {
  if (throughputOf(r) > throughputOf(best)) best = r;
}

md += "\n## 拐点判读\n\n";
md += `- 最高吞吐点：Consumer=${best.consumer_workers}，tx/s=${throughputOf(best)}\n`;

const margins = [];
for (let i = 1; i < enriched.length; i++) {
  const prev = enriched[i - 1];
  const cur = enriched[i];
  const prevRate = throughputOf(prev);
  const curRate = throughputOf(cur);
  const gain = prevRate > 0 ? (curRate - prevRate) / prevRate : 0;
  margins.push({
    from: prev.consumer_workers,
    to: cur.consumer_workers,
    gain_pct: Number((gain * 100).toFixed(1)),
    tx_per_sec: curRate,
    commit_p95: cur.commit?.p95_ms,
    rush_p95: cur.rush?.p95_ms,
  });
}
md += "\n| 段 | 吞吐增益 | 到达 tx/s | commit P95 | rush P95 |\n";
md += "| --- | ---: | ---: | ---: | ---: |\n";
for (const m of margins) {
  md += `| ${m.from}→${m.to} | ${m.gain_pct}% | ${m.tx_per_sec} | ${m.commit_p95 ?? "-"} | ${m.rush_p95 ?? "-"} |\n`;
}

const peak = throughputOf(best);
const near = enriched.filter((r) => throughputOf(r) >= peak * 0.95);
const knee = near[0] || best;
md += `\n建议观察点（非自动生产值）：\n`;
md += `- 峰值：C=${best.consumer_workers}\n`;
md += `- ≥95% 峰值的最小 C：${knee.consumer_workers}（tx/s=${throughputOf(knee)}）\n`;
md += `- 若 rush P95 随 C 明显回升，拐点应取回升前一档\n`;
md += `- 本矩阵在 log_bin=OFF 诊断栈上；不是生产默认耐久性\n`;

const outPath = path.join(outDir, "sweep-summary.md");
fs.writeFileSync(outPath, md, "utf8");
fs.writeFileSync(
  path.join(outDir, "sweep-enriched.json"),
  JSON.stringify({ peak: best, knee, margins, rows: enriched }, null, 2),
);
console.log(md);
