import fs from "node:fs";

function parseTsv(path) {
  const text = fs.readFileSync(path, "utf8").replace(/^\uFEFF/, "").trim();
  if (!text) return [];
  const lines = text.split(/\r?\n/);
  const headers = lines[0].split("\t");
  return lines.slice(1).map((line) => {
    const cols = line.split("\t");
    const row = {};
    headers.forEach((h, i) => {
      row[h] = cols[i];
    });
    return {
      digest: row.digest_text || row.DIGEST_TEXT || "",
      schema: row.SCHEMA_NAME || row.schema_name || "",
      count: Number(row.COUNT_STAR || 0),
      total_s: Number(row.total_s || 0),
      avg_ms: Number(row.avg_ms || 0),
      max_ms: Number(row.max_ms || 0),
      rows_examined: Number(row.SUM_ROWS_EXAMINED || 0),
      rows_sent: Number(row.SUM_ROWS_SENT || 0),
      no_index: Number(row.SUM_NO_INDEX_USED || 0),
      tmp_tables: Number(row.SUM_CREATED_TMP_TABLES || 0),
    };
  });
}

function keyOf(r) {
  return `${r.schema}||${r.digest}`;
}

const beforePath = process.argv[2];
const afterPath = process.argv[3];
const outPath = process.argv[4];
const before = new Map(parseTsv(beforePath).map((r) => [keyOf(r), r]));
const after = parseTsv(afterPath);
const deltas = [];
for (const a of after) {
  const b = before.get(keyOf(a)) || {
    count: 0,
    total_s: 0,
    rows_examined: 0,
    rows_sent: 0,
    no_index: 0,
    tmp_tables: 0,
  };
  const count = a.count - b.count;
  const total_s = a.total_s - b.total_s;
  if (count <= 0 && total_s <= 0.0001) continue;
  deltas.push({
    digest: a.digest,
    schema: a.schema,
    count,
    total_s: Number(total_s.toFixed(6)),
    avg_ms: count > 0 ? Number(((total_s * 1000) / count).toFixed(3)) : a.avg_ms,
    max_ms: a.max_ms,
    rows_examined: a.rows_examined - b.rows_examined,
    rows_sent: a.rows_sent - b.rows_sent,
    no_index: a.no_index - b.no_index,
    tmp_tables: a.tmp_tables - b.tmp_tables,
  });
}
deltas.sort((x, y) => y.total_s - x.total_s);
fs.writeFileSync(outPath, JSON.stringify(deltas.slice(0, 80), null, 2));
console.log(JSON.stringify(deltas.slice(0, 25), null, 2));
