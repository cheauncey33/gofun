#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";

const outDir = process.argv[2];
if (!outDir) {
  console.error("usage: node render_durability_matrix.mjs <output-dir>");
  process.exit(1);
}

const matrix = JSON.parse(
  fs.readFileSync(path.join(outDir, "matrix.json"), "utf8").replace(/^\uFEFF/, ""),
);
const cells = Array.isArray(matrix) ? matrix : [matrix];

const titles = {
  "1plus1_baseline": "1+1 安全基线",
  "2plus1_no_redo_fsync": "2+1 去掉每事务 redo fsync",
  "1plus0_no_binlog_fsync": "1+0 去掉每事务 binlog fsync",
  "2plus0_ceiling": "2+0 接近持久化天花板",
};

let md = "";
md += "# Durability 诊断矩阵\n\n";
md += "固定探针：6 workers 并发 COMMIT；durability 仅运行时 SET GLOBAL，测完恢复 1+1。\n\n";
md += "| 配置 | flush_log | sync_binlog | 单线程 wall/commit | 并发 wall P50 | P95 | P99 | commits/s | log_fsync/s | log_fsync/commit | redo wait(s) | binlog wait(s) |\n";
md += "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n";

for (const cell of cells) {
  const w = cell.concurrent.wall_per_commit_ms || {};
  const title = titles[cell.label] || cell.label;
  md += `| ${title} | ${cell.innodb_flush_log_at_trx_commit} | ${cell.sync_binlog} | ${cell.single_thread_wall_ms_per_commit} | ${w.p50_ms} | ${w.p95_ms} | ${w.p99_ms} | ${cell.concurrent.commits_per_sec} | ${cell.concurrent.innodb_os_log_fsyncs_per_sec} | ${cell.concurrent.log_fsyncs_per_commit} | ${cell.concurrent.redo_wait_total_s} | ${cell.concurrent.binlog_wait_total_s} |\n`;
}

const byLabel = Object.fromEntries(cells.map((c) => [c.label, c]));
const b = byLabel["1plus1_baseline"];
const r = byLabel["2plus1_no_redo_fsync"];
const n = byLabel["1plus0_no_binlog_fsync"];
const c = byLabel["2plus0_ceiling"];
if (b && r && n && c) {
  const p50 = (x) => x.concurrent.wall_per_commit_ms.p50_ms;
  md += "\n## 归因（并发 wall P50）\n\n";
  md += `| 对比 | P50(ms) | delta vs 1+1 | 解读 |\n`;
  md += `| --- | ---: | ---: | --- |\n`;
  md += `| 1+1 基线 | ${p50(b)} | 0 | 双 fsync |\n`;
  md += `| 2+1 | ${p50(r)} | ${(p50(r) - p50(b)).toFixed(2)} | 去掉每事务 redo fsync |\n`;
  md += `| 1+0 | ${p50(n)} | ${(p50(n) - p50(b)).toFixed(2)} | 去掉每事务 binlog fsync |\n`;
  md += `| 2+0 | ${p50(c)} | ${(p50(c) - p50(b)).toFixed(2)} | 双 fsync 都去掉后的天花板 |\n`;
  md += `\n- redo 贡献 ≈ 1+1 P50 - 2+1 P50 = ${(p50(b) - p50(r)).toFixed(2)} ms\n`;
  md += `- binlog 贡献 ≈ 1+1 P50 - 1+0 P50 = ${(p50(b) - p50(n)).toFixed(2)} ms\n`;
  md += `- 双 fsync 合计贡献 ≈ 1+1 P50 - 2+0 P50 = ${(p50(b) - p50(c)).toFixed(2)} ms\n`;
}

const consumerPath = path.join(outDir, "consumer_matrix.json");
if (fs.existsSync(consumerPath)) {
  const rows = JSON.parse(
    fs.readFileSync(consumerPath, "utf8").replace(/^\uFEFF/, ""),
  );
  md += "\n## Consumer spot（真实 stage=commit 增量）\n\n";
  md += "| 配置 | flush | sync | commit P50 | P95 | P99 | avg | count | drain_s |\n";
  md += "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n";
  for (const row of rows) {
    const cc = row.consumer_commit || {};
    const title = titles[row.label] || row.label;
    md += `| ${title} | ${row.flush} | ${row.sync} | ${cc.p50_ms} | ${cc.p95_ms} | ${cc.p99_ms} | ${cc.avg_ms} | ${cc.count} | ${row.drain_seconds} |\n`;
  }
}

md += "\n## 读法\n\n";
md += "- `1+1 -> 2+1`：每事务 redo fsync 贡献\n";
md += "- `1+1 -> 1+0`：每事务 binlog fsync 贡献\n";
md += "- `2+0`：接近解除每事务持久化约束后的性能天花板（仍可能有写缓冲、组提交、CPU）\n";
md += "- 诊断用途，不作为生产默认配置建议\n";

fs.writeFileSync(path.join(outDir, "matrix.md"), md, "utf8");
console.log(md);
