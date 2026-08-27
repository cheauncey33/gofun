import fs from "node:fs";

function routeStats(promPath) {
  const prom = JSON.parse(fs.readFileSync(promPath, "utf8").replace(/^\uFEFF/, ""));
  const routes = {};
  for (const [k, v] of Object.entries(prom)) {
    let m = k.match(
      /^http_request_duration_seconds_count\{method="([^"]+)",path="([^"]+)"\}$/,
    );
    if (m) {
      const key = `${m[1]} ${m[2]}`;
      routes[key] = routes[key] || {};
      routes[key].count = v;
    }
    m = k.match(
      /^http_request_duration_seconds_sum\{method="([^"]+)",path="([^"]+)"\}$/,
    );
    if (m) {
      const key = `${m[1]} ${m[2]}`;
      routes[key] = routes[key] || {};
      routes[key].sum = v;
    }
  }
  return Object.entries(routes)
    .filter(([k]) => k.includes("/api/v1/") && !k.includes("organizers") && !k.includes("admin"))
    .map(([k, o]) => ({
      route: k,
      count: Math.round(o.count || 0),
      avg_ms: o.count ? Number((((o.sum || 0) / o.count) * 1000).toFixed(1)) : 0,
    }))
    .sort((a, b) => b.avg_ms - a.avg_ms);
}

const base = process.argv[2];
const a = routeStats(`${base}/rate-600/prom-after.json`);
const b = routeStats(`${base}/rate-800/prom-after.json`);
const map600 = Object.fromEntries(a.map((x) => [x.route, x]));
const map800 = Object.fromEntries(b.map((x) => [x.route, x]));
const keys = [...new Set([...a, ...b].map((x) => x.route))];
const rows = keys
  .map((k) => ({
    route: k,
    c600: map600[k]?.count || 0,
    avg600: map600[k]?.avg_ms || 0,
    c800: map800[k]?.count || 0,
    avg800: map800[k]?.avg_ms || 0,
    ratio:
      map600[k]?.avg_ms > 0
        ? Number(((map800[k]?.avg_ms || 0) / map600[k].avg_ms).toFixed(1))
        : null,
  }))
  .sort((x, y) => (y.avg800 || 0) - (x.avg800 || 0));
fs.writeFileSync(`${base}/http-route-avg.json`, JSON.stringify(rows, null, 2));
console.log(JSON.stringify(rows, null, 2));
