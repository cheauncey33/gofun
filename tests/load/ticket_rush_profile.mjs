import { spawn } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";

const runLabel = process.env.RUN_LABEL || `run-${new Date().toISOString().replaceAll(":", "-")}`;
const outputDir = process.env.RESULTS_DIR || join("tests", "load", "results", runLabel);
const pprofURL = process.env.PPROF_URL || "http://127.0.0.1:6060/debug/pprof";
const metricsURL = process.env.METRICS_URL || "http://127.0.0.1:8080/metrics";
const profileSeconds = Number(process.env.PROFILE_SECONDS || process.env.DURATION_SECONDS || 60);

if (!process.env.RUSH_CAMPAIGN_ID) {
  throw new Error("set RUSH_CAMPAIGN_ID before profiling");
}

await mkdir(outputDir, { recursive: true });
await snapshotText(metricsURL, join(outputDir, "metrics-before.prom"));

const profilePromise = snapshotBinary(
  `${pprofURL}/profile?seconds=${profileSeconds}`,
  join(outputDir, "cpu.pprof"),
);
const loadPromise = runNode("tests/load/ticket_rush_spike.mjs", {
  ...process.env,
  RESULT_FILE: join(outputDir, "load.json"),
});

const [profileResult, loadExitCode] = await Promise.allSettled([profilePromise, loadPromise]);
if (loadExitCode.status === "rejected" || loadExitCode.value !== 0) {
  throw new Error(`load process failed: ${loadExitCode.reason || `exit ${loadExitCode.value}`}`);
}
if (profileResult.status === "rejected") {
  console.error(`cpu profile capture failed (load still valid): ${profileResult.reason}`);
}

try {
  await snapshotBinary(`${pprofURL}/heap`, join(outputDir, "heap.pprof"));
} catch (err) {
  console.error(`heap profile capture failed: ${err.message}`);
}
try {
  await snapshotText(metricsURL, join(outputDir, "metrics-after.prom"));
} catch (err) {
  console.error(`metrics-after capture failed: ${err.message}`);
}
await writeFile(join(outputDir, "manifest.json"), `${JSON.stringify({
  run_label: runLabel,
  created_at: new Date().toISOString(),
  base_url: process.env.BASE_URL || "http://127.0.0.1:8080/api/v1",
  pprof_url: pprofURL,
  metrics_url: metricsURL,
  campaign_id: process.env.RUSH_CAMPAIGN_ID,
  concurrency: Number(process.env.CONCURRENCY || 50),
  duration_seconds: Number(process.env.DURATION_SECONDS || 60),
  profile_seconds: profileSeconds,
  think_ms: Number(process.env.THINK_MS || 0),
}, null, 2)}\n`, "utf8");

console.log(`profile artifacts written to ${outputDir}`);

async function snapshotText(url, file) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`GET ${url}: HTTP ${res.status}`);
  await writeFile(file, await res.text(), "utf8");
}

async function snapshotBinary(url, file) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`GET ${url}: HTTP ${res.status}`);
  await writeFile(file, Buffer.from(await res.arrayBuffer()));
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
