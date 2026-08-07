/**
 * 扫 WAVE_SIZE：验证入口 success_qps 是否随发压并发上升。
 *
 * 用法（需已有可压测后端，默认 :18080）：
 *   $env:BASE_URL='http://127.0.0.1:18080/api/v1'
 *   $env:METRICS_URL='http://127.0.0.1:18080/metrics'
 *   $env:STEPS='1500'
 *   $env:RUNS='1'
 *   $env:WAVE_STEPS='50,200,500,1000'
 *   $env:RUN_LABEL='wave-sweep-1500'
 *   node tests/load/ticket_rush_wave_sweep.mjs
 *
 * 每档会单独起一次 staircase（独立用户池/活动），结果写入
 * tests/load/results/<RUN_LABEL>/wave-<N>/ 并汇总 wave-sweep-summary.md
 */
import { spawn } from "node:child_process";
import { mkdir, writeFile, readFile } from "node:fs/promises";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const waveSteps = String(process.env.WAVE_STEPS || "50,200,500,1000")
  .split(",")
  .map(Number)
  .filter((v) => Number.isInteger(v) && v > 0);
const steps = process.env.STEPS || "1500";
const runs = process.env.RUNS || "1";
const baseLabel = process.env.RUN_LABEL || `wave-sweep-${Date.now()}`;
const resultsRoot = join(root, "tests", "load", "results", baseLabel);

if (waveSteps.length === 0) {
  throw new Error("WAVE_STEPS must contain positive integers");
}

await mkdir(resultsRoot, { recursive: true });

const rows = [];

for (const wave of waveSteps) {
  const runLabel = `${baseLabel}/wave-${wave}`;
  const resultsDir = join(root, "tests", "load", "results", runLabel);
  console.error(`\n=== WAVE_SIZE=${wave} STEPS=${steps} RUNS=${runs} ===`);

  const env = {
    ...process.env,
    STEPS: steps,
    RUNS: runs,
    WAVE_SIZE: String(wave),
    RUN_LABEL: runLabel,
    RESULTS_DIR: resultsDir,
    BASE_URL: process.env.BASE_URL || "http://127.0.0.1:18080/api/v1",
    METRICS_URL: process.env.METRICS_URL || "http://127.0.0.1:18080/metrics",
  };

  const code = await runNode(join(root, "tests", "load", "ticket_rush_staircase.mjs"), env);
  if (code !== 0) {
    throw new Error(`staircase failed for WAVE_SIZE=${wave}, exit=${code}`);
  }

  const summaryPath = join(resultsDir, "staircase-summary.json");
  const summary = JSON.parse(await readFile(summaryPath, "utf8"));
  const results = summary.results || [];
  const qps = median(results.map((r) => r.success_qps));
  const p99 = median(results.map((r) => r.execute?.latency_ms?.p99));
  const drain = median(results.map((r) => r.drain_seconds));
  const success = median(results.map((r) => r.success_orders));
  const executeSeconds = median(results.map((r) => r.execute_seconds));

  rows.push({
    wave,
    success_orders_median: success,
    execute_seconds_median: executeSeconds,
    success_qps_median: qps,
    execute_p99_median: p99,
    drain_seconds_median: drain,
    runs: results.length,
  });

  console.error(
    `WAVE=${wave}: qps=${qps} p99=${p99}ms drain=${drain}s success=${success} execute_s=${executeSeconds}`,
  );
}

const md = [
  `# WAVE_SIZE 扫档（STEPS=${steps}）`,
  "",
  `- 标签：\`${baseLabel}\``,
  `- 公式：\`success_qps = success_orders / execute_seconds\``,
  `- 发压：按波串行，每波 \`WAVE_SIZE\` 并发 \`rush execute\``,
  "",
  "| WAVE_SIZE | 成功单数 | execute 秒 | success_qps | execute p99 | MQ 追平 |",
  "| ---: | ---: | ---: | ---: | ---: | ---: |",
  ...rows.map(
    (r) =>
      `| ${r.wave} | ${r.success_orders_median} | ${r.execute_seconds_median} | ${r.success_qps_median} | ${r.execute_p99_median}ms | ${r.drain_seconds_median}s |`,
  ),
  "",
  "## 解读提示",
  "",
  "- 若 QPS 随 WAVE 明显上升：此前被客户端 wave 卡住，不是服务端天花板。",
  "- 若 WAVE 增大后 QPS 平台/下降、p99 恶化：接近入口瓶颈（限流/Redis/本机 Docker）。",
  "",
].join("\n");

await writeFile(join(resultsRoot, "wave-sweep-summary.json"), `${JSON.stringify({ config: { steps, runs, waveSteps }, rows }, null, 2)}\n`);
await writeFile(join(resultsRoot, "wave-sweep-summary.md"), `${md}\n`);
console.log(JSON.stringify({ config: { steps, runs, waveSteps }, rows }, null, 2));
console.error(`\nWrote ${join(resultsRoot, "wave-sweep-summary.md")}`);

function median(values) {
  const nums = values.map(Number).filter((v) => Number.isFinite(v)).sort((a, b) => a - b);
  if (nums.length === 0) return null;
  const mid = Math.floor(nums.length / 2);
  return nums.length % 2 ? nums[mid] : Number(((nums[mid - 1] + nums[mid]) / 2).toFixed(2));
}

function runNode(script, env) {
  return new Promise((resolve) => {
    const child = spawn(process.execPath, [script], {
      cwd: root,
      env,
      stdio: "inherit",
    });
    child.on("exit", (code) => resolve(code ?? 1));
  });
}
