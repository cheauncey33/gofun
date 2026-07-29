/**
 * 同参数多轮压测：输出每轮 QPS/延迟 + 跨轮中位数与极差。
 * 用法：
 *   RUNS=3 DURATION_SECONDS=60 CONCURRENCY=20 RUSH_CAMPAIGN_ID=... \
 *   RUN_LABEL=variance-before node tests/load/ticket_rush_multirun.mjs
 */
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { spawn } from "node:child_process";

const runs = Number(process.env.RUNS || 3);
const runLabel = process.env.RUN_LABEL || `multirun-${Date.now()}`;
const baseDir = join("tests", "load", "results", runLabel);
const duration = Number(process.env.DURATION_SECONDS || 60);
const concurrency = Number(process.env.CONCURRENCY || 20);

if (!process.env.RUSH_CAMPAIGN_ID) {
  throw new Error("set RUSH_CAMPAIGN_ID");
}

await mkdir(baseDir, { recursive: true });
const rounds = [];

for (let i = 1; i <= runs; i += 1) {
  const label = `${runLabel}-r${i}`;
  const outDir = join(baseDir, `r${i}`);
  console.error(`\n=== ${label} (${i}/${runs}) ===`);
  const code = await runNode("tests/load/ticket_rush_profile.mjs", {
    ...process.env,
    RUN_LABEL: label,
    RESULTS_DIR: outDir,
    DURATION_SECONDS: String(duration),
    PROFILE_SECONDS: String(duration),
    CONCURRENCY: String(concurrency),
    LOAD_USER_PREFIX: `${process.env.LOAD_USER_PREFIX || "mr_"}${i}_`,
  });
  if (code !== 0) throw new Error(`run ${i} failed with exit ${code}`);
  const load = JSON.parse(await readFile(join(outDir, "load.json"), "utf8"));
  rounds.push({
    round: i,
    qps: load.requests_per_second,
    avg: load.latency_ms.avg,
    p50: load.latency_ms.p50,
    p95: load.latency_ms.p95,
    p99: load.latency_ms.p99,
    total: load.total_requests,
    outcomes: load.outcomes,
    statuses: load.statuses,
  });
}

const summary = {
  run_label: runLabel,
  created_at: new Date().toISOString(),
  campaign_id: process.env.RUSH_CAMPAIGN_ID,
  concurrency,
  duration_seconds: duration,
  runs,
  rounds,
  aggregate: {
    qps: agg(rounds.map((r) => r.qps)),
    avg_ms: agg(rounds.map((r) => r.avg)),
    p50_ms: agg(rounds.map((r) => r.p50)),
    p95_ms: agg(rounds.map((r) => r.p95)),
    p99_ms: agg(rounds.map((r) => r.p99)),
  },
};

await writeFile(join(baseDir, "summary.json"), `${JSON.stringify(summary, null, 2)}\n`);
console.log(JSON.stringify(summary, null, 2));

function agg(values) {
  const sorted = values.slice().sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  const median = sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
  return {
    values: sorted,
    min: sorted[0],
    max: sorted.at(-1),
    median: Number(median.toFixed(2)),
    range: Number((sorted.at(-1) - sorted[0]).toFixed(2)),
  };
}

function runNode(script, env) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [script], {
      cwd: process.cwd(),
      env,
      stdio: "inherit",
    });
    child.once("error", reject);
    child.once("exit", (code) => resolve(code ?? 1));
  });
}
