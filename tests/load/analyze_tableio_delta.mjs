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
      table: row.table_name || row.OBJECT_NAME || "",
      count_star: Number(row.COUNT_STAR || 0),
      count_fetch: Number(row.COUNT_FETCH || 0),
      count_insert: Number(row.COUNT_INSERT || 0),
      count_update: Number(row.COUNT_UPDATE || 0),
      count_delete: Number(row.COUNT_DELETE || 0),
      total_s: Number(row.total_s || 0),
      fetch_s: Number(row.fetch_s || 0),
      insert_s: Number(row.insert_s || 0),
      update_s: Number(row.update_s || 0),
      delete_s: Number(row.delete_s || 0),
    };
  });
}

const beforePath = process.argv[2];
const afterPath = process.argv[3];
const outPath = process.argv[4];
const before = new Map(parseTsv(beforePath).map((r) => [r.table, r]));
const after = parseTsv(afterPath);
const deltas = [];
for (const a of after) {
  const b = before.get(a.table) || {
    count_star: 0,
    count_fetch: 0,
    count_insert: 0,
    count_update: 0,
    count_delete: 0,
    total_s: 0,
    fetch_s: 0,
    insert_s: 0,
    update_s: 0,
    delete_s: 0,
  };
  const total_s = a.total_s - b.total_s;
  const count_star = a.count_star - b.count_star;
  if (count_star <= 0 && total_s <= 0.0001) continue;
  deltas.push({
    table: a.table,
    count_star,
    count_fetch: a.count_fetch - b.count_fetch,
    count_insert: a.count_insert - b.count_insert,
    count_update: a.count_update - b.count_update,
    count_delete: a.count_delete - b.count_delete,
    total_s: Number(total_s.toFixed(6)),
    fetch_s: Number((a.fetch_s - b.fetch_s).toFixed(6)),
    insert_s: Number((a.insert_s - b.insert_s).toFixed(6)),
    update_s: Number((a.update_s - b.update_s).toFixed(6)),
    delete_s: Number((a.delete_s - b.delete_s).toFixed(6)),
  });
}
deltas.sort((x, y) => y.total_s - x.total_s);
fs.writeFileSync(outPath, JSON.stringify(deltas, null, 2));
console.log(JSON.stringify(deltas.slice(0, 20), null, 2));
