param(
  [int]$Rate = 1000,
  [string]$Duration = "60s",
  [int]$ConsumerWorkers = 8,
  [string]$Project = "gofun-csweep",
  [string]$BackendPort = "18590",
  [string]$OutputDir = "tests/load/results/mixed-1000-db-rank-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [switch]$SkipLogBin,
  [switch]$KeepStack
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$baseCompose = Join-Path $repo "tests\integration\docker-compose.ticketing.yml"
$capacityCompose = Join-Path $repo "tests\load\docker-compose.capacity.yml"
$nobinlogCompose = Join-Path $repo "tests\load\docker-compose.nobinlog.yml"
$outputPath = Join-Path $repo $OutputDir
New-Item -ItemType Directory -Force -Path $outputPath | Out-Null

$rabbitContainer = "${Project}-rabbitmq-1"
$mysqlContainer = "${Project}-mysql-1"
$backendContainer = "${Project}-backend-load"
$redisContainer = "${Project}-redis-1"
$baseUrl = "http://127.0.0.1:$BackendPort/api/v1"
$metricsUrl = "http://127.0.0.1:$BackendPort/metrics"
$k6Network = "${Project}_default"
$k6BaseUrl = "http://backend:8080/api/v1"

$composeArgs = @("compose", "-p", $Project, "-f", $baseCompose, "-f", $capacityCompose)
if ($SkipLogBin) { $composeArgs += @("-f", $nobinlogCompose) }

function Invoke-Compose([string[]]$Arguments) {
  & docker @composeArgs @Arguments
  if ($LASTEXITCODE -ne 0) { throw "docker compose failed: $LASTEXITCODE" }
}

function Wait-ForBackend {
  $deadline = [DateTime]::UtcNow.AddSeconds(180)
  do {
    try {
      if ((Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$BackendPort/healthz" -TimeoutSec 3).StatusCode -eq 200) { return }
    } catch {}
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  throw "backend health timeout"
}

function Set-Env {
  $env:CAPACITY_CONTAINER_PREFIX = $Project
  $env:GOFUN_BACKEND_PORT = $BackendPort
  $env:GOFUN_ELASTICSEARCH_PORT = "19610"
  $env:GOFUN_MYSQL_PORT = "13330"
  $env:GOFUN_REDIS_PORT = "16410"
  $env:GOFUN_RABBITMQ_PORT = "26080"
  $env:GOFUN_RABBITMQ_MANAGEMENT_PORT = "36080"
  $env:INVENTORY_BUCKETS_ENABLED = "true"
  $env:INVENTORY_BUCKET_COUNT = "32"
  $env:INVENTORY_MIN_QUOTA_TO_BUCKET = "64"
  $env:INVENTORY_BUCKET_RETRY = "4"
  $env:ORDER_CONSUMER_WORKER_COUNT = [string]$ConsumerWorkers
  $env:ORDER_CONSUMER_PREFETCH_COUNT = "5"
  $env:ORDER_CONSUMER_MAX_RETRIES = "3"
  $env:DELAYED_ORDER_WORKER_COUNT = "2"
  $env:ORDER_OUTBOX_PUBLISH_WORKERS = "4"
  $env:ORDER_OUTBOX_PUBLISH_BATCH = "200"
  $env:MYSQL_MAX_OPEN_CONNS = "100"
  $env:MYSQL_MAX_IDLE_CONNS = "10"
  $env:REDIS_POOL_SIZE = "100"
  $env:RATELIMIT_DISTRIBUTED_WRITE_ENABLED = "true"
  $env:RABBITMQ_QUEUE_TYPE = "classic"
  $env:MYSQL_CONTAINER = $mysqlContainer
  $env:K6_JWT_SECRET = "fuchang-integration-test-secret-only"
  $env:TELEMETRY_ENABLED = "false"
}

function Clear-Residue {
  $sql = @"
SET FOREIGN_KEY_CHECKS=0;
TRUNCATE TABLE ticket_verification_record;
TRUNCATE TABLE admission_ticket;
TRUNCATE TABLE ticket_order_attendee;
TRUNCATE TABLE ticket_order_item;
TRUNCATE TABLE ticket_order_outbox;
TRUNCATE TABLE payment_callback;
TRUNCATE TABLE payment_transaction;
TRUNCATE TABLE ticket_order;
TRUNCATE TABLE rush_campaign_bucket;
TRUNCATE TABLE ticket_tier_bucket;
TRUNCATE TABLE rush_sale_campaign;
TRUNCATE TABLE ticket_tier;
TRUNCATE TABLE event_session;
TRUNCATE TABLE event;
SET FOREIGN_KEY_CHECKS=1;
"@
  cmd /c "docker exec -e MYSQL_PWD=fuchang-it-mysql $mysqlContainer mysql -ufuchang -N -B fuchang_ticketing_it -e `"$sql`" 2>&1" | Out-Null
  docker exec $redisContainer redis-cli FLUSHDB 2>$null | Out-Null
  foreach ($q in @("fuchang.it.order.queue","fuchang.it.order.retry","fuchang.it.order.dead","fuchang.order.delay","fuchang.order.timeout")) {
    docker exec $rabbitContainer rabbitmqctl purge_queue $q 2>$null | Out-Null
  }
}

function Export-Digest([string]$Path) {
  # No SCHEMA filter / no TRUNCATE — delta later. Include NULL schema digests.
  $sql = @"
SELECT
  COALESCE(SCHEMA_NAME, '') AS SCHEMA_NAME,
  LEFT(DIGEST_TEXT, 240) AS digest_text,
  COUNT_STAR,
  ROUND(SUM_TIMER_WAIT/1e12, 6) AS total_s,
  ROUND(AVG_TIMER_WAIT/1e9, 3) AS avg_ms,
  ROUND(MAX_TIMER_WAIT/1e9, 3) AS max_ms,
  SUM_ROWS_EXAMINED,
  SUM_ROWS_SENT,
  SUM_NO_INDEX_USED,
  SUM_CREATED_TMP_TABLES
FROM performance_schema.events_statements_summary_by_digest
WHERE DIGEST_TEXT IS NOT NULL
  AND DIGEST_TEXT NOT LIKE 'SHOW %'
  AND DIGEST_TEXT NOT LIKE 'SET %'
  AND DIGEST_TEXT NOT LIKE 'SELECT @@%'
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 120;
"@
  docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -B -e $sql 2>$null |
    Set-Content -Encoding utf8 $Path
}

function Export-TableIO([string]$Path) {
  # Cumulative counters — use before/after delta. Prepared-statement digests are unreliable here.
  $sql = @"
SELECT
  OBJECT_NAME AS table_name,
  COUNT_STAR,
  COUNT_FETCH,
  COUNT_INSERT,
  COUNT_UPDATE,
  COUNT_DELETE,
  ROUND(SUM_TIMER_WAIT/1e12, 6) AS total_s,
  ROUND(SUM_TIMER_FETCH/1e12, 6) AS fetch_s,
  ROUND(SUM_TIMER_INSERT/1e12, 6) AS insert_s,
  ROUND(SUM_TIMER_UPDATE/1e12, 6) AS update_s,
  ROUND(SUM_TIMER_DELETE/1e12, 6) AS delete_s
FROM performance_schema.table_io_waits_summary_by_table
WHERE OBJECT_SCHEMA = 'fuchang_ticketing_it'
ORDER BY SUM_TIMER_WAIT DESC;
"@
  docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -B -e $sql 2>$null |
    Set-Content -Encoding utf8 $Path
}

function Get-PrometheusMap {
  $map = @{}
  $body = (Invoke-WebRequest -UseBasicParsing -Uri $metricsUrl -TimeoutSec 5).Content
  foreach ($line in ($body -split "`n")) {
    if ($line -match '^(?<name>[a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(?<labels>[^}]*)\})?\s+(?<value>[-+]?[0-9]*\.?[0-9]+(?:[eE][-+]?[0-9]+)?)') {
      $key = if ([string]::IsNullOrWhiteSpace($Matches.labels)) { $Matches.name } else { "$($Matches.name){$($Matches.labels)}" }
      $map[$key] = [double]$Matches.value
    }
  }
  return $map
}

Set-Env
Write-Host "Rebuilding backend image for cache remodel verify..." -ForegroundColor Cyan
Invoke-Compose @("up", "-d", "--build", "--wait")
Wait-ForBackend
docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=1;" 2>$null | Out-Null
docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "UPDATE performance_schema.setup_consumers SET ENABLED='YES' WHERE NAME IN ('events_statements_current','events_statements_history','events_statements_history_long','statements_digest');" 2>$null | Out-Null

Clear-Residue
Invoke-Compose @("up", "-d", "--no-deps", "--force-recreate", "backend")
Wait-ForBackend

$env:BASE_URL = $baseUrl
$env:K6_USERS = "3000"
$env:RUSH_TOTAL_QUOTA = "200000"
$env:RUSH_PER_USER_LIMIT = "20"
$env:RUSH_LABEL = "db-rank-$Rate-$(Get-Date -Format HHmmss)"
& node (Join-Path $repo "tests\load\k6\prepare_rush_fixture_fast.mjs")
if ($LASTEXITCODE -ne 0) { throw "fixture failed" }

# Warm catalog cache so ranking reflects post-cache residual DB load.
try {
  Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/catalog/meta" -TimeoutSec 5 | Out-Null
  Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/events?city=Wuhan&page=1&page_size=20" -TimeoutSec 5 | Out-Null
  Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/rush-sales" -TimeoutSec 5 | Out-Null
  $fixture = Get-Content (Join-Path $repo "tests\load\k6\fixtures\rush_execute.json") -Raw -Encoding utf8 | ConvertFrom-Json
  if ($fixture.event_id) {
    Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/events/$($fixture.event_id)" -TimeoutSec 5 | Out-Null
  }
} catch {}

Start-Sleep -Seconds 2
Export-Digest (Join-Path $outputPath "digest-before.tsv")
Export-TableIO (Join-Path $outputPath "tableio-before.tsv")
$promBefore = Get-PrometheusMap
$promBefore | ConvertTo-Json -Depth 3 | Set-Content -Encoding utf8 (Join-Path $outputPath "prom-before.json")

$summaryPath = Join-Path $outputPath "k6-summary.json"
$k6Root = Join-Path $repo "tests\load\k6"
$preVUs = [Math]::Max($Rate, 100)
$maxVUs = [Math]::Max($Rate * 2, $preVUs)
$k6Args = @(
  "run", "--rm", "--network", $k6Network, "--workdir", "/work",
  "--volume", "${k6Root}:/work",
  "--volume", "${outputPath}:/results",
  "--env", "BASE_URL=$k6BaseUrl",
  "--env", "K6_SUMMARY=/results/k6-summary.json",
  "--env", "K6_FIXTURE=fixtures/rush_execute.json",
  "--env", "RATE=$Rate",
  "--env", "DURATION=$Duration",
  "--env", "PRE_VUS=$preVUs",
  "--env", "MAX_VUS=$maxVUs",
  "--env", "CITY=Wuhan",
  "grafana/k6:latest", "run", "/work/mixed_traffic.js"
)
$stdout = Join-Path $outputPath "k6.stdout.log"
$stderr = Join-Path $outputPath "k6.stderr.log"
$proc = Start-Process -FilePath "docker" -ArgumentList $k6Args -WorkingDirectory $repo -NoNewWindow -PassThru `
  -RedirectStandardOutput $stdout -RedirectStandardError $stderr

$samples = New-Object System.Collections.Generic.List[object]
$started = Get-Date
while (-not $proc.HasExited) {
  $prom = Get-PrometheusMap
  $tx = 0.0
  foreach ($k in $prom.Keys) { if ($k -like 'ticket_order_consumer_transactions_total*') { $tx += $prom[$k] } }
  $samples.Add([pscustomobject]@{
    elapsed_seconds = [Math]::Round(((Get-Date) - $started).TotalSeconds, 1)
    in_use = if ($prom.ContainsKey('go_sql_db_in_use')) { $prom['go_sql_db_in_use'] } else { 0 }
    wait_count = if ($prom.ContainsKey('go_sql_db_wait_count')) { $prom['go_sql_db_wait_count'] } else { 0 }
    wait_dur = if ($prom.ContainsKey('go_sql_db_wait_duration_seconds')) { $prom['go_sql_db_wait_duration_seconds'] } else { 0 }
    consumer_tx = $tx
  })
  Start-Sleep -Seconds 1
  $proc.Refresh()
}
$samples | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 (Join-Path $outputPath "samples.json")

Export-Digest (Join-Path $outputPath "digest-after.tsv")
Export-TableIO (Join-Path $outputPath "tableio-after.tsv")
$promAfter = Get-PrometheusMap
$promAfter | ConvertTo-Json -Depth 3 | Set-Content -Encoding utf8 (Join-Path $outputPath "prom-after.json")

& node (Join-Path $repo "tests\load\analyze_digest_delta.mjs") `
  (Join-Path $outputPath "digest-before.tsv") `
  (Join-Path $outputPath "digest-after.tsv") `
  (Join-Path $outputPath "digest-delta.json")

& node (Join-Path $repo "tests\load\analyze_tableio_delta.mjs") `
  (Join-Path $outputPath "tableio-before.tsv") `
  (Join-Path $outputPath "tableio-after.tsv") `
  (Join-Path $outputPath "tableio-delta.json")

& node --input-type=module -e @"
import fs from 'fs';
const outDir = process.argv[1];
const prom = JSON.parse(fs.readFileSync(outDir + '/prom-after.json','utf8').replace(/^\uFEFF/,''));
const before = JSON.parse(fs.readFileSync(outDir + '/prom-before.json','utf8').replace(/^\uFEFF/,''));
const digests = JSON.parse(fs.readFileSync(outDir + '/digest-delta.json','utf8'));
const tableio = JSON.parse(fs.readFileSync(outDir + '/tableio-delta.json','utf8'));
const k6 = JSON.parse(fs.readFileSync(outDir + '/k6-summary.json','utf8').replace(/^\uFEFF/,''));
const samples = JSON.parse(fs.readFileSync(outDir + '/samples.json','utf8').replace(/^\uFEFF/,''));

function routeDelta(metricPrefix) {
  const routes = {};
  for (const [k,v] of Object.entries(prom)) {
    const mCount = k.match(/^http_request_duration_seconds_count\{method=\"([^\"]+)\",path=\"([^\"]+)\"\}$/);
    if (mCount) {
      const key = mCount[1] + ' ' + mCount[2];
      routes[key] = routes[key] || {};
      routes[key].count = v - (before[k] || 0);
    }
    const mSum = k.match(/^http_request_duration_seconds_sum\{method=\"([^\"]+)\",path=\"([^\"]+)\"\}$/);
    if (mSum) {
      const key = mSum[1] + ' ' + mSum[2];
      routes[key] = routes[key] || {};
      routes[key].sum = v - (before[k] || 0);
    }
  }
  return Object.entries(routes)
    .filter(([k,o]) => (o.count||0) > 0 && k.includes('/api/v1/'))
    .map(([k,o]) => ({
      route: k,
      count: Math.round(o.count||0),
      total_s: Number((o.sum||0).toFixed(3)),
      avg_ms: o.count ? Number(((o.sum||0)/o.count*1000).toFixed(1)) : 0,
      // rough connection-hold proxy: total HTTP time (assumes most of handler holds DB conn under load)
      hold_share_pct: 0,
    }))
    .sort((a,b) => b.total_s - a.total_s);
}

const routes = routeDelta();
const httpTotalS = routes.reduce((s,r)=>s+r.total_s,0) || 1;
for (const r of routes) r.hold_share_pct = Number((r.total_s / httpTotalS * 100).toFixed(1));
// register is bcrypt-bound: exclude from DB-hold proxy.
const dbProxyRoutes = routes.filter(r => r.route !== 'POST /api/v1/register');
const dbProxyTotal = dbProxyRoutes.reduce((s,r)=>s+r.total_s,0) || 1;
for (const r of dbProxyRoutes) r.db_hold_share_pct = Number((r.total_s / dbProxyTotal * 100).toFixed(1));

const focus = [
  'GET /api/v1/orders',
  'GET /api/v1/orders/:id',
  'POST /api/v1/login',
  'POST /api/v1/register',
  'POST /api/v1/orders',
  'POST /api/v1/rush-sales/:id/execute',
  'GET /api/v1/events',
  'GET /api/v1/events/:id',
  'GET /api/v1/rush-sales',
  'GET /api/v1/catalog/meta',
];
const focusRows = focus.map(f => routes.find(r => r.route === f) || { route: f, count: 0, total_s: 0, avg_ms: 0, hold_share_pct: 0 });

const wait0 = before['go_sql_db_wait_count'] || 0;
const wait1 = prom['go_sql_db_wait_count'] || 0;
const waitD0 = before['go_sql_db_wait_duration_seconds'] || 0;
const waitD1 = prom['go_sql_db_wait_duration_seconds'] || 0;
const inUsePeak = Math.max(...samples.map(s => s.in_use||0));
const tx0 = samples[0]?.consumer_tx || 0;
const tx1 = samples[samples.length-1]?.consumer_tx || 0;
const dt = Math.max(0.001, (samples[samples.length-1]?.elapsed_seconds||1) - (samples[0]?.elapsed_seconds||0));

function classifyDigest(d) {
  const t = (d.digest||'').toUpperCase();
  if (t.includes('COMMIT')) return 'commit';
  if (t.includes('START TRANSACTION') || t.includes('BEGIN')) return 'begin';
  if (t.includes('TICKET_ORDER_ITEM') || t.includes('TICKET_ORDER ')) {
    if (t.startsWith('SELECT') && t.includes('USER_ID')) return 'order_list_or_detail';
    if (t.startsWith('SELECT')) return 'order_select';
    if (t.startsWith('INSERT')) return 'order_insert';
    if (t.startsWith('UPDATE')) return 'order_update';
  }
  if (t.includes('RUSH_CAMPAIGN') || t.includes('RUSH_SALE')) return 'rush';
  if (t.includes('TICKET_TIER') || t.includes('EVENT_SESSION') || t.includes('`EVENT`') || t.includes(' VENUE')) return 'catalog';
  if (t.includes('`USER`') || t.includes(' FROM USER')) return 'auth_user';
  if (t.includes('TICKET_ORDER_OUTBOX') || t.includes('OUTBOX')) return 'outbox';
  if (t.includes('PAYMENT')) return 'payment';
  return 'other';
}

const digestClass = {};
for (const d of digests) {
  const c = classifyDigest(d);
  digestClass[c] = digestClass[c] || { total_s: 0, count: 0 };
  digestClass[c].total_s += d.total_s;
  digestClass[c].count += d.count;
}
const classRank = Object.entries(digestClass)
  .map(([k,v]) => ({ class: k, total_s: Number(v.total_s.toFixed(3)), count: v.count }))
  .sort((a,b)=>b.total_s-a.total_s);

const tableioTotal = tableio.reduce((s,t)=>s+t.total_s,0) || 1;
const tableioFetch = tableio.reduce((s,t)=>s+t.fetch_s,0) || 1;
const tableioWrite = tableio.reduce((s,t)=>s+t.insert_s+t.update_s+t.delete_s,0) || 1;
const tableRows = tableio.slice(0, 15).map(t => ({
  ...t,
  share_pct: Number((t.total_s / tableioTotal * 100).toFixed(1)),
  fetch_share_pct: Number((t.fetch_s / tableioFetch * 100).toFixed(1)),
  write_share_pct: Number(((t.insert_s+t.update_s+t.delete_s) / tableioWrite * 100).toFixed(1)),
}));

const report = {
  rate: Number(process.env.RATE || 1000),
  k6: { ach: k6.http_req_rate, p50: k6.p50_ms, p95: k6.p95_ms, p99: k6.p99_ms },
  pool: {
    in_use_peak: inUsePeak,
    wait_count_delta: wait1 - wait0,
    wait_duration_delta_s: Number((waitD1 - waitD0).toFixed(3)),
    avg_wait_ms: (wait1-wait0) > 0 ? Number(((waitD1-waitD0)/(wait1-wait0)*1000).toFixed(1)) : 0,
  },
  consumer_tx_per_sec: Number(((tx1-tx0)/dt).toFixed(1)),
  route_rank_all: routes.slice(0, 15),
  route_focus: focusRows,
  route_db_proxy: dbProxyRoutes.slice(0, 12),
  tableio_rank: tableRows,
  tableio_totals: {
    total_s: Number(tableioTotal.toFixed(3)),
    fetch_s: Number(tableioFetch.toFixed(3)),
    write_s: Number(tableioWrite.toFixed(3)),
  },
  digest_class_rank: classRank,
  digest_top: digests.slice(0, 25),
  note: 'statement digests underprepared for Go prepared statements; table_io + HTTP route used for ranking',
};
fs.writeFileSync(outDir + '/DB_RANK.json', JSON.stringify(report, null, 2));

let md = '# Mixed 1000 QPS DB rank (catalog cache ON)\n\n';
md += '| metric | value |\n| --- | ---: |\n';
md += '| ach rps | ' + Number(k6.http_req_rate).toFixed(1) + ' |\n';
md += '| HTTP P95/P99 | ' + Number(k6.p95_ms).toFixed(1) + ' / ' + Number(k6.p99_ms).toFixed(1) + ' |\n';
md += '| cons tx/s | ' + report.consumer_tx_per_sec + ' |\n';
md += '| in_use peak | ' + inUsePeak + '/100 |\n';
md += '| wait Δ / avg wait | ' + report.pool.wait_count_delta + ' / ' + report.pool.avg_wait_ms + ' ms |\n';
md += '| table_io total/fetch/write | ' + report.tableio_totals.total_s + ' / ' + report.tableio_totals.fetch_s + ' / ' + report.tableio_totals.write_s + ' s |\n\n';
md += '## HTTP route DB-hold proxy (excludes register/bcrypt)\n\n';
md += '| route | count | avg_ms | total_s | db_share% |\n| --- | ---: | ---: | ---: | ---: |\n';
for (const r of dbProxyRoutes.slice(0, 12)) {
  md += '| ' + r.route + ' | ' + r.count + ' | ' + r.avg_ms + ' | ' + r.total_s + ' | ' + r.db_hold_share_pct + ' |\n';
}
md += '\n## Focus routes (raw HTTP time; register is CPU not DB)\n\n';
md += '| route | count | avg_ms | total_s | raw_share% |\n| --- | ---: | ---: | ---: | ---: |\n';
for (const r of focusRows) {
  md += '| ' + r.route + ' | ' + r.count + ' | ' + r.avg_ms + ' | ' + r.total_s + ' | ' + r.hold_share_pct + ' |\n';
}
md += '\n## Table IO rank (best SQL proxy; digests incomplete)\n\n';
md += '| table | total_s | share% | fetch_s | insert_s | update_s | fetch# | insert# | update# |\n| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n';
for (const t of tableRows) {
  md += '| ' + t.table + ' | ' + t.total_s + ' | ' + t.share_pct + ' | ' + t.fetch_s + ' | ' + t.insert_s + ' | ' + t.update_s + ' | ' + t.count_fetch + ' | ' + t.count_insert + ' | ' + t.count_update + ' |\n';
}
md += '\n## SQL digest class (often only COMMIT visible)\n\n| class | total_s | count |\n| --- | ---: | ---: |\n';
for (const c of classRank) md += '| ' + c.class + ' | ' + c.total_s + ' | ' + c.count + ' |\n';
md += '\n## Decision hints\n\n';
md += '- Orders GET share high + ticket_order fetch high => order query cache / read replica / trim Preload.\n';
md += '- ticket_order insert/update + COMMIT dominate + pool wait => isolate Consumer pool before raising MaxOpenConns.\n';
md += '- register avg ~hundreds ms => bcrypt CPU; do not size DB pool for it.\n';
md += '- catalog tables still high fetch => cache miss / short TTL.\n';
fs.writeFileSync(outDir + '/DB_RANK.md', md);
console.log(md);
"@ $outputPath

Write-Host ("DB rank: " + (Join-Path $outputPath "DB_RANK.md")) -ForegroundColor Green
if (-not $KeepStack) { Invoke-Compose @("down", "-v", "--remove-orphans") }
