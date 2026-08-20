import fs from "node:fs";
import path from "node:path";

const HTTP_PATH = "/api/v1/rush-sales/:id/execute";

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, "utf8").replace(/^\uFEFF/, ""));
}

function parseLabels(key) {
  const start = key.indexOf("{");
  if (start < 0) return {};
  const labels = {};
  const body = key.slice(start + 1, -1);
  for (const match of body.matchAll(/([a-zA-Z_][a-zA-Z0-9_]*)="((?:\\.|[^"])*)"/g)) {
    labels[match[1]] = match[2].replace(/\\"/g, '"').replace(/\\\\/g, "\\");
  }
  return labels;
}

function matchesLabels(actual, expected) {
  return Object.entries(expected).every(([key, value]) => actual[key] === value);
}

function histogramDelta(before, after, metric, requiredLabels = {}) {
  const buckets = [];
  let count = 0;
  let sum = 0;

  for (const [key, value] of Object.entries(after ?? {})) {
    if (!key.startsWith(`${metric}_`)) continue;
    const labels = parseLabels(key);
    if (!matchesLabels(labels, requiredLabels)) continue;
    const delta = Number(value) - Number(before?.[key] ?? 0);
    if (key.startsWith(`${metric}_count`)) count = delta;
    if (key.startsWith(`${metric}_sum`)) sum = delta;
    if (key.startsWith(`${metric}_bucket`) && labels.le != null) {
      buckets.push({
        le: labels.le === "+Inf" ? Infinity : Number(labels.le),
        count: delta,
      });
    }
  }

  buckets.sort((left, right) => left.le - right.le);
  return { count, sum, buckets };
}

function percentileMs(histogram, percentile) {
  if (histogram.count <= 0 || histogram.buckets.length === 0) return null;
  const target = histogram.count * (percentile / 100);
  let previousUpperBound = 0;
  let previousCount = 0;

  for (const bucket of histogram.buckets) {
    if (bucket.count >= target) {
      if (!Number.isFinite(bucket.le)) return previousUpperBound * 1000;
      if (bucket.count === previousCount) return bucket.le * 1000;
      const fraction = (target - previousCount) / (bucket.count - previousCount);
      return (previousUpperBound + fraction * (bucket.le - previousUpperBound)) * 1000;
    }
    if (Number.isFinite(bucket.le)) previousUpperBound = bucket.le;
    previousCount = bucket.count;
  }
  return previousUpperBound * 1000;
}

function summarizeHistogram(before, after, metric, labels) {
  const histogram = histogramDelta(before, after, metric, labels);
  return {
    count: Math.round(histogram.count),
    p99_ms: histogram.count > 0 ? Number(percentileMs(histogram, 99).toFixed(3)) : null,
  };
}

function normalizeSamples(value) {
  return Array.isArray(value) ? value : [value];
}

function loadRound(runDir) {
  const summary = readJson(path.join(runDir, "run-summary.json"));
  const samples = normalizeSamples(readJson(path.join(runDir, "lifecycle-samples.json")))
    .filter((sample) => sample?.prometheus && Object.keys(sample.prometheus).length > 0);
  if (samples.length < 2) {
    throw new Error(`${runDir}: lifecycle-samples.json 至少需要两个 Prometheus 快照`);
  }

  const before = samples[0].prometheus;
  const after = samples.at(-1).prometheus;
  const http = summarizeHistogram(before, after, "http_request_duration_seconds", {
    method: "POST",
    path: HTTP_PATH,
  });
  const accepted = summarizeHistogram(
    before,
    after,
    "ticket_order_accepted_to_pending_payment_duration_seconds",
  );
  const consumer = summarizeHistogram(
    before,
    after,
    "ticket_order_consumer_transaction_duration_seconds",
    { result: "success" },
  );

  const missing = [
    ["HTTP", http],
    ["accepted_to_pending_payment", accepted],
    ["consumer_transaction", consumer],
  ].filter(([, metric]) => metric.count <= 0);
  if (missing.length > 0) {
    throw new Error(`${runDir}: 缺少本轮 Histogram 样本：${missing.map(([name]) => name).join(", ")}`);
  }

  const correctness = {
    http_success_rate: Number(summary.http_success_rate),
    primary_drained: summary.primary_drained === true,
    drain_completed: summary.drain_completed === true,
    dead_letters_after: Number(summary.dead_letters_after),
    pending_outbox_after: Number(summary.outbox_pending_after),
    work_queue_after: Number(summary.mq_work_queue_after),
    orders_after: Number(summary.orders_after),
  };
  correctness.passed =
    correctness.http_success_rate === 1 &&
    correctness.primary_drained &&
    correctness.drain_completed &&
    correctness.dead_letters_after === 0 &&
    correctness.pending_outbox_after === 0 &&
    correctness.work_queue_after === 0;

  return {
    run_dir: path.resolve(runDir),
    scenario: {
      vus: Number(summary.vus),
      consumer_workers: Number(summary.consumer_workers),
      bucket_count: Number(summary.bucket_count),
    },
    throughput_rps: Number(Number(summary.http_req_rate).toFixed(3)),
    http,
    accepted_to_pending_payment: accepted,
    consumer_transaction: consumer,
    mq_drain_seconds: Number(summary.drain_seconds),
    correctness,
  };
}

function median(values) {
  const sorted = values.filter(Number.isFinite).sort((left, right) => left - right);
  if (sorted.length === 0) return null;
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 1
    ? sorted[middle]
    : Number(((sorted[middle - 1] + sorted[middle]) / 2).toFixed(3));
}

function ensureSameScenario(rounds) {
  const expected = JSON.stringify(rounds[0].scenario);
  const mismatch = rounds.find((round) => JSON.stringify(round.scenario) !== expected);
  if (mismatch) throw new Error("所有轮次必须使用相同的 VUS、Consumer worker 和库存分桶配置");
}

function buildReport(rounds) {
  ensureSameScenario(rounds);
  return {
    generated_at: new Date().toISOString(),
    scope: {
      endpoint: `POST ${HTTP_PATH}`,
      percentile_source: "Prometheus cumulative histogram delta",
      aggregation: "same scenario, median across rounds",
      required_rounds: 3,
    },
    scenario: rounds[0].scenario,
    valid_rounds: rounds.length,
    all_correct: rounds.every((round) => round.correctness.passed),
    median: {
      throughput_rps: median(rounds.map((round) => round.throughput_rps)),
      http_p99_ms: median(rounds.map((round) => round.http.p99_ms)),
      accepted_to_pending_payment_p99_ms: median(
        rounds.map((round) => round.accepted_to_pending_payment.p99_ms),
      ),
      consumer_transaction_p99_ms: median(rounds.map((round) => round.consumer_transaction.p99_ms)),
      mq_drain_seconds: median(rounds.map((round) => round.mq_drain_seconds)),
    },
    rounds,
  };
}

function renderMarkdown(report) {
  const lines = [
    "# Gofun 核心链路性能报告",
    "",
    `- 场景：VUS=${report.scenario.vus}，Consumer=${report.scenario.consumer_workers}，库存分桶=${report.scenario.bucket_count}`,
    `- 轮次：${report.valid_rounds}/3；最终一致性：${report.all_correct ? "通过" : "未通过"}`,
    "- p99 口径：服务端 Prometheus Histogram 的本轮增量；三轮取中位数。",
    "",
    "| 核心指标 | 三轮中位数 |",
    "| --- | ---: |",
    `| HTTP 吞吐 | ${report.median.throughput_rps} req/s |`,
    `| 抢票入口 p99 | ${report.median.http_p99_ms} ms |`,
    `| 受理到 pending_payment p99 | ${report.median.accepted_to_pending_payment_p99_ms} ms |`,
    `| Consumer 事务 p99 | ${report.median.consumer_transaction_p99_ms} ms |`,
    `| MQ 追平 | ${report.median.mq_drain_seconds} s |`,
    "",
    "| 轮次 | req/s | HTTP p99 | 受理到 pending p99 | Consumer p99 | MQ 追平 | 正确性 |",
    "| ---: | ---: | ---: | ---: | ---: | ---: | :---: |",
  ];
  report.rounds.forEach((round, index) => {
    lines.push(
      `| ${index + 1} | ${round.throughput_rps} | ${round.http.p99_ms} ms | ${round.accepted_to_pending_payment.p99_ms} ms | ${round.consumer_transaction.p99_ms} ms | ${round.mq_drain_seconds} s | ${round.correctness.passed ? "通过" : "失败"} |`,
    );
  });
  lines.push(
    "",
    "正确性要求：HTTP 成功率 100%、工作队列与 Outbox 归零、无死信，且追平完成。该报告只代表记录的机器、配置和数据规模。",
    "",
  );
  return lines.join("\n");
}

function selfTest() {
  const before = {};
  const after = {
    'demo_bucket{le="0.1"}': 50,
    'demo_bucket{le="0.5"}': 99,
    'demo_bucket{le="+Inf"}': 100,
    demo_count: 100,
    demo_sum: 20,
  };
  const result = summarizeHistogram(before, after, "demo", {});
  if (result.count !== 100 || result.p99_ms !== 500) throw new Error("histogram self-test failed");
  console.log("render_core_performance_report self-test passed");
}

const args = process.argv.slice(2);
if (args[0] === "--self-test") {
  selfTest();
  process.exit(0);
}

const outputIndex = args.indexOf("--output");
const output = outputIndex >= 0 ? args[outputIndex + 1] : "core-performance-report.md";
const runDirs = args.filter((value, index) => index !== outputIndex && index !== outputIndex + 1);
if (runDirs.length !== 3) {
  console.error("usage: node render_core_performance_report.mjs [--output report.md] <round-1> <round-2> <round-3>");
  console.error("核心报告固定接收同一场景的 3 个运行目录");
  process.exit(2);
}

const report = buildReport(runDirs.map(loadRound));
const outputPath = path.resolve(output);
fs.writeFileSync(outputPath, renderMarkdown(report), "utf8");
fs.writeFileSync(outputPath.replace(/\.md$/i, ".json"), `${JSON.stringify(report, null, 2)}\n`, "utf8");
console.log(`核心性能报告已生成：${outputPath}`);
