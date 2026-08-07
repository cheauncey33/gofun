/**
 * k6 峰值阶梯：对同一夹具连续打多档 VUS，汇总对比。
 *
 * 用法：
 *   # 先 prepare 夹具
 *   node tests/load/k6/prepare_rush_fixture.mjs
 *   # 再扫峰值
 *   $env:PEAK_VUS='50,100,200,500,1000'
 *   $env:PEAK_DURATION='20s'
 *   node tests/load/k6/run_peak_sweep.mjs
 */
import { spawn } from "node:child_process";
import { mkdir, writeFile, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..");
const peakVUs = String(process.env.PEAK_VUS || "50,100,200,500,1000")
  .split(",")
  .map(Number)
  .filter((v) => Number.isInteger(v) && v > 0);
const duration = process.env.PEAK_DURATION || "20s";
const script = join(root, "tests", "load", "k6", "rush_execute.js");
const label = process.env.RUN_LABEL || `k6-peak-${Date.now()}`;
const outDir = join(root, "tests", "load", "results", label);

if (peakVUs.length === 0) throw new Error("PEAK_VUS must contain positive integers");

await mkdir(outDir, { recursive: true });
const rows = [];

for (const vus of peakVUs) {
  // 每档重新 prepare，避免上一档限购/库存干扰峰值读数
  if (process.env.PEAK_REPREPARE !== "0") {
    console.error(`\n=== prepare fixture before VUS=${vus} ===`);
    const prepCode = await run(process.execPath, [
      join(root, "tests", "load", "k6", "prepare_rush_fixture.mjs"),
    ], {
      ...process.env,
      BASE_URL: process.env.BASE_URL || "http://127.0.0.1:18080/api/v1",
      // 个人限购上限 20：用户数需覆盖「预估总请求/20」。
      // 粗估每 VU 每秒 ≤15 次，再留余量；下限 2000。
      K6_USERS:
        process.env.K6_USERS ||
        String(
          Math.max(
            2000,
            Math.ceil(
              (vus * Number(String(duration).replace(/[^0-9.]/g, "") || 20) * 15) / 20,
            ),
          ),
        ),
      RUSH_TOTAL_QUOTA: process.env.RUSH_TOTAL_QUOTA || "200000",
      RUSH_PER_USER_LIMIT: process.env.RUSH_PER_USER_LIMIT || "20",
      SETUP_CONCURRENCY: process.env.SETUP_CONCURRENCY || "25",
    });
    if (prepCode !== 0) throw new Error(`prepare failed before VUS=${vus}`);
  }

  const summaryPath = join(outDir, `vus-${vus}.json`);
  console.error(`\n=== k6 peak VUS=${vus} DURATION=${duration} ===`);
  const code = await run("k6", [
    "run",
    "-e", `VUS=${vus}`,
    "-e", `DURATION=${duration}`,
    "-e", `K6_SUMMARY=${summaryPath.replace(/\\/g, "/")}`,
    script,
  ], {
    ...process.env,
    Path: process.env.Path,
  });
  if (code !== 0) {
    console.error(`k6 exited ${code} for VUS=${vus} (still collecting summary if present)`);
  }

  let summary = null;
  try {
    summary = JSON.parse(await readFile(summaryPath, "utf8"));
  } catch (err) {
    summary = { error: String(err) };
  }
  rows.push({
    vus,
    duration,
    http_req_rate: summary.http_req_rate ?? null,
    p50_ms: summary.p50_ms ?? null,
    p90_ms: summary.p90_ms ?? null,
    p95_ms: summary.p95_ms ?? null,
    p99_ms: summary.p99_ms ?? null,
    avg_ms: summary.avg_ms ?? null,
    success_rate: summary.rush_execute_success_rate ?? null,
    sold_out: summary.rush_execute_sold_out ?? null,
    rejected: summary.rush_execute_rejected ?? null,
    http_reqs: summary.http_reqs ?? null,
  });
  console.error(
    `VUS=${vus}: rate=${rows.at(-1).http_req_rate?.toFixed?.(1) ?? rows.at(-1).http_req_rate} ` +
      `p99=${rows.at(-1).p99_ms} success=${rows.at(-1).success_rate}`,
  );
}

const md = [
  `# k6 峰值阶梯`,
  "",
  `- 标签：\`${label}\``,
  `- 每档时长：\`${duration}\``,
  `- 每档前重新 prepare 夹具：\`${process.env.PEAK_REPREPARE !== "0"}\``,
  "",
  "| VUS | req/s | p90 | p95 | p99 | avg | 成功率 | sold_out | rejected |",
  "| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
  ...rows.map((r) =>
    `| ${r.vus} | ${fmt(r.http_req_rate)} | ${fmt(r.p90_ms)} | ${fmt(r.p95_ms)} | ${fmt(r.p99_ms)} | ${fmt(r.avg_ms)} | ${fmtRate(r.success_rate)} | ${r.sold_out ?? "-"} | ${r.rejected ?? "-"} |`,
  ),
  "",
  "## 读法",
  "",
  "- **峰值吞吐**：成功率仍高时的最大 `http_req_rate`",
  "- **合格峰值**：建议同时满足成功率 ≥ 99% 且 p99 ≤ 300ms（或你定的 SLO）",
  "- sold_out 升高通常是限购/库存，不是 5xx；rejected 才是限流/服务端错误",
  "",
].join("\n");

await writeFile(join(outDir, "peak-summary.json"), `${JSON.stringify({ label, duration, rows }, null, 2)}\n`);
await writeFile(join(outDir, "peak-summary.md"), `${md}\n`);
console.log(JSON.stringify({ label, outDir, rows }, null, 2));
console.error(`\nWrote ${join(outDir, "peak-summary.md")}`);

function fmt(v) {
  if (v == null || Number.isNaN(Number(v))) return "-";
  return Number(v).toFixed(1);
}
function fmtRate(v) {
  if (v == null || Number.isNaN(Number(v))) return "-";
  return `${(Number(v) * 100).toFixed(1)}%`;
}
function run(cmd, args, env) {
  return new Promise((resolve) => {
    const child = spawn(cmd, args, {
      cwd: root,
      env,
      stdio: "inherit",
      shell: process.platform === "win32",
    });
    child.on("exit", (code) => resolve(code ?? 1));
  });
}
