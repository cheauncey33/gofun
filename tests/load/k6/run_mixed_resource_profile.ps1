param(
  [string]$Rates = "600,800",
  [string]$Duration = "60s",
  [int]$ConsumerWorkers = 8,
  [int]$InventoryBucketCount = 32,
  [int]$OutboxPublishWorkers = 4,
  [int]$HttpMaxOpenConns = 100,
  [int]$WorkerMaxOpenConns = 0,
  [int]$InnoDBFlushLogAtTrxCommit = 1,
  [switch]$SkipLogBin,
  [string]$Project = "gofun-csweep",
  [string]$BackendPort = "18590",
  [string]$OutputDir = "tests/load/results/mixed-resource-profile-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [switch]$KeepStack,
  [string]$K6Image = "grafana/k6:latest"
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
$rateList = @($Rates.Split(",") | ForEach-Object { [int]$_.Trim() } | Where-Object { $_ -gt 0 })

$composeArgs = @("compose", "-p", $Project, "-f", $baseCompose, "-f", $capacityCompose)
if ($SkipLogBin) { $composeArgs += @("-f", $nobinlogCompose) }

function Invoke-Compose([string[]]$Arguments) {
  & docker @composeArgs @Arguments
  if ($LASTEXITCODE -ne 0) { throw "docker compose failed: $LASTEXITCODE" }
}

function Wait-ForBackend {
  $deadline = [DateTime]::UtcNow.AddSeconds(180)
  do {
    $state = (docker inspect -f "{{.State.Status}}|{{.State.ExitCode}}" $backendContainer 2>$null)
    if ($state -match '^exited\|') { throw "backend exited: $state" }
    try {
      if ((Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$BackendPort/healthz" -TimeoutSec 3).StatusCode -eq 200) { return }
    } catch {}
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  throw "backend health timeout"
}

function Set-CommonEnv {
  $env:CAPACITY_CONTAINER_PREFIX = $Project
  $env:GOFUN_BACKEND_PORT = $BackendPort
  $env:GOFUN_ELASTICSEARCH_PORT = "19610"
  $env:GOFUN_MYSQL_PORT = "13330"
  $env:GOFUN_REDIS_PORT = "16410"
  $env:GOFUN_RABBITMQ_PORT = "26080"
  $env:GOFUN_RABBITMQ_MANAGEMENT_PORT = "36080"
  $env:INVENTORY_BUCKETS_ENABLED = "true"
  $env:INVENTORY_BUCKET_COUNT = [string]$InventoryBucketCount
  $env:INVENTORY_MIN_QUOTA_TO_BUCKET = "64"
  $env:INVENTORY_BUCKET_RETRY = "4"
  $env:ORDER_CONSUMER_WORKER_COUNT = [string]$ConsumerWorkers
  $env:ORDER_CONSUMER_PREFETCH_COUNT = "5"
  $env:ORDER_CONSUMER_MAX_RETRIES = "3"
  $env:DELAYED_ORDER_WORKER_COUNT = "2"
  $env:ORDER_OUTBOX_PUBLISH_WORKERS = [string]$OutboxPublishWorkers
  $env:ORDER_OUTBOX_PUBLISH_BATCH = "200"
  $env:MYSQL_MAX_OPEN_CONNS = [string]$HttpMaxOpenConns
  $env:MYSQL_MAX_IDLE_CONNS = "10"
  $env:MYSQL_WORKER_MAX_OPEN_CONNS = [string]$WorkerMaxOpenConns
  $env:MYSQL_WORKER_MAX_IDLE_CONNS = "10"
  $env:REDIS_POOL_SIZE = "100"
  $env:RATELIMIT_DISTRIBUTED_WRITE_ENABLED = "true"
  $env:RABBITMQ_QUEUE_TYPE = "classic"
  $env:MYSQL_CONTAINER = $mysqlContainer
  $env:K6_JWT_SECRET = "fuchang-integration-test-secret-only"
  $env:TELEMETRY_ENABLED = "false"
}

function Invoke-Mysql([string]$Query) {
  @(docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e $Query 2>$null)
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

function Get-PrometheusMap {
  $map = @{}
  try {
    $body = (Invoke-WebRequest -UseBasicParsing -Uri $metricsUrl -TimeoutSec 5).Content
    foreach ($line in ($body -split "`n")) {
      if ($line -match '^(?<name>[a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(?<labels>[^}]*)\})?\s+(?<value>[-+]?[0-9]*\.?[0-9]+(?:[eE][-+]?[0-9]+)?)') {
        $key = if ([string]::IsNullOrWhiteSpace($Matches.labels)) { $Matches.name } else { "$($Matches.name){$($Matches.labels)}" }
        $map[$key] = [double]$Matches.value
      }
    }
  } catch {}
  return $map
}

function Get-Prom([hashtable]$Map, [string]$Name) {
  if ($Map.ContainsKey($Name)) { return [double]$Map[$Name] }
  return 0
}

function Get-PoolProm([hashtable]$Map, [string]$Metric, [string]$Pool) {
  $key = '{0}{{pool="{1}"}}' -f $Metric, $Pool
  return Get-Prom $Map $key
}

function Get-RabbitReady {
  $rows = @(docker exec $rabbitContainer rabbitmqctl list_queues name messages_ready --quiet 2>$null)
  foreach ($row in $rows) {
    $parts = @(($row -split "\s+") | Where-Object { $_ -ne "" })
    if ($parts.Count -ge 2 -and $parts[0] -eq "fuchang.it.order.queue") { return [int64]$parts[1] }
  }
  return 0
}

function Export-Digest([string]$Path) {
  $sql = @"
SELECT
  LEFT(DIGEST_TEXT, 220) AS digest_text,
  COUNT_STAR,
  ROUND(SUM_TIMER_WAIT/1e12, 6) AS total_s,
  ROUND(AVG_TIMER_WAIT/1e9, 3) AS avg_ms,
  ROUND(MAX_TIMER_WAIT/1e9, 3) AS max_ms,
  SUM_ROWS_EXAMINED,
  SUM_ROWS_SENT,
  SUM_NO_INDEX_USED,
  SUM_CREATED_TMP_TABLES
FROM performance_schema.events_statements_summary_by_digest
WHERE SCHEMA_NAME = 'fuchang_ticketing_it'
  AND DIGEST_TEXT IS NOT NULL
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 80;
"@
  $out = docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -B -e $sql 2>$null
  $out | Set-Content -Encoding utf8 $Path
}

function Export-MySQLStatus([string]$Path) {
  $vars = @(
    "Threads_running","Threads_connected","Innodb_row_lock_waits","Innodb_row_lock_time",
    "Innodb_buffer_pool_read_requests","Innodb_buffer_pool_reads",
    "Innodb_data_reads","Innodb_data_writes","Questions","Slow_queries"
  )
  $rows = @()
  foreach ($v in $vars) {
    $val = @(Invoke-Mysql "SHOW GLOBAL STATUS LIKE '$v';")
    if ($val.Count -gt 0) {
      $parts = @(($val[0] -split "\s+") | Where-Object { $_ -ne "" })
      if ($parts.Count -ge 2) { $rows += [pscustomobject]@{ name = $parts[0]; value = [int64]$parts[1] } }
    }
  }
  $rows | ConvertTo-Json | Set-Content -Encoding utf8 $Path
  return $rows
}

function Get-DockerStats([string]$Name) {
  $line = docker stats $Name --no-stream --format "{{.CPUPerc}}|{{.MemUsage}}|{{.NetIO}}|{{.BlockIO}}" 2>$null
  if (-not $line) { return $null }
  $p = $line -split '\|'
  [pscustomobject]@{
    cpu_perc = ($p[0] -replace '%','')
    mem = $p[1]
    net_io = $p[2]
    block_io = $p[3]
  }
}

Set-CommonEnv
Write-Host "Rebuilding backend with DB pool metrics..." -ForegroundColor Cyan
Invoke-Compose @("up", "-d", "--build", "--wait")
Wait-ForBackend
docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit;" 2>$null | Out-Null
Invoke-Mysql @"
UPDATE performance_schema.setup_consumers
SET ENABLED = 'YES'
WHERE NAME IN ('events_statements_current','events_statements_history','events_statements_history_long','statements_digest');
"@ | Out-Null

$profileRows = @()
foreach ($rate in $rateList) {
  Write-Host "===== resource profile rate=$rate =====" -ForegroundColor Cyan
  $runDir = Join-Path $outputPath ("rate-" + $rate)
  New-Item -ItemType Directory -Force -Path $runDir | Out-Null

  Clear-Residue
  Invoke-Compose @("up", "-d", "--no-deps", "--force-recreate", "backend")
  Wait-ForBackend
  docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit;" 2>$null | Out-Null
  Start-Sleep -Seconds 3

  $env:BASE_URL = $baseUrl
  $env:K6_USERS = "3000"
  $env:RUSH_TOTAL_QUOTA = "200000"
  $env:RUSH_PER_USER_LIMIT = "20"
  $env:RUSH_LABEL = "mixed-res-$rate-$(Get-Date -Format HHmmss)"
  & node (Join-Path $repo "tests\load\k6\prepare_rush_fixture_fast.mjs")
  if ($LASTEXITCODE -ne 0) { throw "fixture failed" }
  $fixturePath = Join-Path $repo "tests\load\k6\fixtures\rush_execute.json"
  $fixture = Get-Content $fixturePath -Raw -Encoding utf8 | ConvertFrom-Json
  if (-not $fixture.city) {
    $fixture | Add-Member -NotePropertyName city -NotePropertyValue "Wuhan" -Force
    [System.IO.File]::WriteAllText($fixturePath, (($fixture | ConvertTo-Json -Depth 8) + "`n"), [System.Text.UTF8Encoding]::new($false))
  }

  # Reset digest counters for clean delta (best-effort).
  Invoke-Mysql "TRUNCATE TABLE performance_schema.events_statements_summary_by_digest;" | Out-Null
  Export-Digest (Join-Path $runDir "digest-before.tsv")
  $statusBefore = Export-MySQLStatus (Join-Path $runDir "mysql-status-before.json")
  $promBefore = Get-PrometheusMap
  $promBefore | ConvertTo-Json -Depth 4 | Set-Content -Encoding utf8 (Join-Path $runDir "prom-before.json")

  $samples = [System.Collections.Generic.List[object]]::new()
  $started = Get-Date
  $summaryPath = Join-Path $runDir "k6-summary.json"
  $k6Stdout = Join-Path $runDir "k6.stdout.log"
  $k6Stderr = Join-Path $runDir "k6.stderr.log"
  $k6Root = Join-Path $repo "tests\load\k6"
  $preVUs = [Math]::Max($rate, 80)
  $maxVUs = [Math]::Max($rate * 2, $preVUs)
  $k6Args = @(
    "run", "--rm", "--network", $k6Network, "--workdir", "/work",
    "--volume", "${k6Root}:/work",
    "--volume", "${runDir}:/results",
    "--env", "BASE_URL=$k6BaseUrl",
    "--env", "K6_SUMMARY=/results/k6-summary.json",
    "--env", "K6_FIXTURE=fixtures/rush_execute.json",
    "--env", "RATE=$rate",
    "--env", "DURATION=$Duration",
    "--env", "PRE_VUS=$preVUs",
    "--env", "MAX_VUS=$maxVUs",
    "--env", "CITY=Wuhan",
    $K6Image, "run", "/work/mixed_traffic.js"
  )
  $process = Start-Process -FilePath "docker" -ArgumentList $k6Args -WorkingDirectory $repo -NoNewWindow -PassThru `
    -RedirectStandardOutput $k6Stdout -RedirectStandardError $k6Stderr

  while (-not $process.HasExited) {
    $prom = Get-PrometheusMap
    $sample = [pscustomobject]@{
      timestamp = (Get-Date).ToString("o")
      elapsed_seconds = [Math]::Round(((Get-Date) - $started).TotalSeconds, 1)
      order_ready = Get-RabbitReady
      go_sql_in_use = Get-Prom $prom "go_sql_db_in_use"
      go_sql_open = Get-Prom $prom "go_sql_db_open_connections"
      go_sql_idle = Get-Prom $prom "go_sql_db_idle"
      go_sql_wait_count = Get-Prom $prom "go_sql_db_wait_count"
      go_sql_wait_duration_seconds = Get-Prom $prom "go_sql_db_wait_duration_seconds"
      go_sql_max_open = Get-Prom $prom "go_sql_db_max_open_connections"
      http_pool_in_use = Get-PoolProm $prom "go_sql_db_pool_in_use" "http"
      http_pool_wait_count = Get-PoolProm $prom "go_sql_db_pool_wait_count" "http"
      http_pool_wait_duration_seconds = Get-PoolProm $prom "go_sql_db_pool_wait_duration_seconds" "http"
      http_pool_max_open = Get-PoolProm $prom "go_sql_db_pool_max_open_connections" "http"
      worker_pool_in_use = Get-PoolProm $prom "go_sql_db_pool_in_use" "worker"
      worker_pool_wait_count = Get-PoolProm $prom "go_sql_db_pool_wait_count" "worker"
      worker_pool_wait_duration_seconds = Get-PoolProm $prom "go_sql_db_pool_wait_duration_seconds" "worker"
      worker_pool_max_open = Get-PoolProm $prom "go_sql_db_pool_max_open_connections" "worker"
      mysql_threads_running = Get-Prom $prom "mysql_threads_running"
      mysql_threads_connected = Get-Prom $prom "mysql_threads_connected"
      consumer_tx_total = 0.0
      backend_stats = Get-DockerStats $backendContainer
      mysql_stats = Get-DockerStats $mysqlContainer
    }
    foreach ($k in $prom.Keys) {
      if ($k -like 'ticket_order_consumer_transactions_total*') { $sample.consumer_tx_total += [double]$prom[$k] }
    }
    $samples.Add($sample)
    Start-Sleep -Seconds 1
    $process.Refresh()
  }
  $process.Refresh()
  $samples | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $runDir "resource-samples.json")

  Export-Digest (Join-Path $runDir "digest-after.tsv")
  $statusAfter = Export-MySQLStatus (Join-Path $runDir "mysql-status-after.json")
  $promAfter = Get-PrometheusMap
  $promAfter | ConvertTo-Json -Depth 4 | Set-Content -Encoding utf8 (Join-Path $runDir "prom-after.json")

  if (-not (Test-Path $summaryPath)) { throw "k6 summary missing for rate=$rate" }
  $k6 = Get-Content $summaryPath -Raw -Encoding utf8 | ConvertFrom-Json

  # HTTP route latency from Prometheus histograms is hard; use k6 action tags via stdout metrics if present.
  $waitCountDelta = [Math]::Round((Get-Prom $promAfter "go_sql_db_wait_count") - (Get-Prom $promBefore "go_sql_db_wait_count"), 0)
  $waitDurDelta = [Math]::Round((Get-Prom $promAfter "go_sql_db_wait_duration_seconds") - (Get-Prom $promBefore "go_sql_db_wait_duration_seconds"), 3)
  $httpWaitDelta = [Math]::Round((Get-PoolProm $promAfter "go_sql_db_pool_wait_count" "http") - (Get-PoolProm $promBefore "go_sql_db_pool_wait_count" "http"), 0)
  $httpWaitDurDelta = [Math]::Round((Get-PoolProm $promAfter "go_sql_db_pool_wait_duration_seconds" "http") - (Get-PoolProm $promBefore "go_sql_db_pool_wait_duration_seconds" "http"), 3)
  $workerWaitDelta = [Math]::Round((Get-PoolProm $promAfter "go_sql_db_pool_wait_count" "worker") - (Get-PoolProm $promBefore "go_sql_db_pool_wait_count" "worker"), 0)
  $workerWaitDurDelta = [Math]::Round((Get-PoolProm $promAfter "go_sql_db_pool_wait_duration_seconds" "worker") - (Get-PoolProm $promBefore "go_sql_db_pool_wait_duration_seconds" "worker"), 3)
  $inUsePeak = ($samples | Measure-Object -Property go_sql_in_use -Maximum).Maximum
  $openPeak = ($samples | Measure-Object -Property go_sql_open -Maximum).Maximum
  $httpInUsePeak = ($samples | Measure-Object -Property http_pool_in_use -Maximum).Maximum
  $workerInUsePeak = ($samples | Measure-Object -Property worker_pool_in_use -Maximum).Maximum
  $threadsRunPeak = ($samples | Measure-Object -Property mysql_threads_running -Maximum).Maximum
  $threadsConnPeak = ($samples | Measure-Object -Property mysql_threads_connected -Maximum).Maximum
  $readyPeak = ($samples | Measure-Object -Property order_ready -Maximum).Maximum
  $readyEnd = if ($samples.Count) { [int64]$samples[-1].order_ready } else { 0 }
  $readySlope = 0.0
  if ($samples.Count -ge 2) {
    $tail = @($samples | Where-Object { $_.elapsed_seconds -ge ([double]$samples[-1].elapsed_seconds - 15) })
    if ($tail.Count -ge 2) {
      $dtReady = [Math]::Max(0.001, [double]$tail[-1].elapsed_seconds - [double]$tail[0].elapsed_seconds)
      $readySlope = [Math]::Round(([double]$tail[-1].order_ready - [double]$tail[0].order_ready) / $dtReady, 2)
    }
  }
  $tx0 = if ($samples.Count) { [double]$samples[0].consumer_tx_total } else { 0 }
  $tx1 = if ($samples.Count) { [double]$samples[-1].consumer_tx_total } else { 0 }
  $dt = if ($samples.Count -ge 2) { [Math]::Max(0.001, [double]$samples[-1].elapsed_seconds - [double]$samples[0].elapsed_seconds) } else { 1 }
  $consRate = [Math]::Round(($tx1 - $tx0) / $dt, 1)

  $bpReq0 = ($statusBefore | Where-Object name -eq "Innodb_buffer_pool_read_requests").value
  $bpRead0 = ($statusBefore | Where-Object name -eq "Innodb_buffer_pool_reads").value
  $bpReq1 = ($statusAfter | Where-Object name -eq "Innodb_buffer_pool_read_requests").value
  $bpRead1 = ($statusAfter | Where-Object name -eq "Innodb_buffer_pool_reads").value
  $bpReqDelta = [int64]$bpReq1 - [int64]$bpReq0
  $bpReadDelta = [int64]$bpRead1 - [int64]$bpRead0
  $hitRate = if ($bpReqDelta -gt 0) { [Math]::Round(1.0 - ($bpReadDelta / $bpReqDelta), 6) } else { 0 }

  $row = [ordered]@{
    target_rate = $rate
    http_max_open = $HttpMaxOpenConns
    worker_max_open = $WorkerMaxOpenConns
    achieved_req_rate = [Math]::Round([double]$k6.http_req_rate, 1)
    http_p50_ms = [Math]::Round([double]$k6.p50_ms, 1)
    http_p95_ms = [Math]::Round([double]$k6.p95_ms, 1)
    http_p99_ms = [Math]::Round([double]$k6.p99_ms, 1)
    consumer_tx_per_sec = $consRate
    order_ready_peak = $readyPeak
    order_ready_end = $readyEnd
    order_ready_slope_per_s = $readySlope
    go_sql_max_open = Get-Prom $promAfter "go_sql_db_max_open_connections"
    go_sql_in_use_peak = $inUsePeak
    go_sql_open_peak = $openPeak
    go_sql_wait_count_delta = $waitCountDelta
    go_sql_wait_duration_delta_s = $waitDurDelta
    http_pool_in_use_peak = $httpInUsePeak
    http_pool_wait_count_delta = $httpWaitDelta
    http_pool_wait_duration_delta_s = $httpWaitDurDelta
    worker_pool_in_use_peak = $workerInUsePeak
    worker_pool_wait_count_delta = $workerWaitDelta
    worker_pool_wait_duration_delta_s = $workerWaitDurDelta
    mysql_threads_running_peak = $threadsRunPeak
    mysql_threads_connected_peak = $threadsConnPeak
    buffer_pool_hit_rate = $hitRate
    buffer_pool_reads_delta = $bpReadDelta
    action_counts = $k6.action_counts
  }
  $row | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $runDir "profile-row.json")
  $profileRows += [pscustomobject]$row
  Write-Host ("rate={0} httpInUse={1}/{2} httpWaitΔ={3} workerInUse={4}/{5} workerWaitΔ={6} cons={7} readyPeak={8} readySlope={9}/s p95={10}" -f `
    $rate, $httpInUsePeak, $HttpMaxOpenConns, $httpWaitDelta, $workerInUsePeak, `
    $(if ($WorkerMaxOpenConns -gt 0) { $WorkerMaxOpenConns } else { $HttpMaxOpenConns }), `
    $workerWaitDelta, $consRate, $readyPeak, $readySlope, $row.http_p95_ms) -ForegroundColor Green

  # Digest top by total_s from after snapshot (truncated before run => absolute ~= delta)
  & node --input-type=module -e @"
import fs from 'fs';
const after = fs.readFileSync(process.argv[1], 'utf8').replace(/^\uFEFF/, '');
const lines = after.trim().split(/\r?\n/);
if (lines.length < 2) { console.log('[]'); process.exit(0); }
const headers = lines[0].split('\t');
const rows = lines.slice(1).map(l => {
  const c = l.split('\t');
  const o = {};
  headers.forEach((h,i) => o[h] = c[i]);
  return {
    digest: o.digest_text || o.DIGEST_TEXT || '',
    count: Number(o.COUNT_STAR||0),
    total_s: Number(o.total_s||0),
    avg_ms: Number(o.avg_ms||0),
    max_ms: Number(o.max_ms||0),
    rows_examined: Number(o.SUM_ROWS_EXAMINED||0),
    no_index: Number(o.SUM_NO_INDEX_USED||0),
    tmp_tables: Number(o.SUM_CREATED_TMP_TABLES||0),
  };
}).sort((a,b)=>b.total_s-a.total_s).slice(0,25);
fs.writeFileSync(process.argv[2], JSON.stringify(rows, null, 2));
console.log(JSON.stringify(rows.slice(0,8), null, 2));
"@ (Join-Path $runDir "digest-after.tsv") (Join-Path $runDir "digest-top.json")
}

$profileRows | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $outputPath "profile-summary.json")

$sb = New-Object System.Text.StringBuilder
[void]$sb.AppendLine("# Mixed resource profile")
[void]$sb.AppendLine("")
[void]$sb.AppendLine(("Fixed: Consumer={0}, bucket={1}, outbox={2}, model=mixed_traffic_v2, HTTP max_open={3}, worker max_open={4}, nobinlog={5}, redo={6}" -f `
  $ConsumerWorkers, $InventoryBucketCount, $OutboxPublishWorkers, $HttpMaxOpenConns, $WorkerMaxOpenConns, [bool]$SkipLogBin, $InnoDBFlushLogAtTrxCommit))
[void]$sb.AppendLine("")
[void]$sb.AppendLine("| rate | ach | P95 | P99 | cons | ready peak | ready slope/s | http in_use | http waitΔ | worker in_use | worker waitΔ | thr_run | bp hit |")
[void]$sb.AppendLine("| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
foreach ($r in $profileRows) {
  [void]$sb.AppendLine(("| {0} | {1} | {2} | {3} | {4} | {5} | {6} | {7} | {8} | {9} | {10} | {11} | {12} |" -f `
    $r.target_rate, $r.achieved_req_rate, $r.http_p95_ms, $r.http_p99_ms, $r.consumer_tx_per_sec, `
    $r.order_ready_peak, $r.order_ready_slope_per_s, $r.http_pool_in_use_peak, $r.http_pool_wait_count_delta, `
    $r.worker_pool_in_use_peak, $r.worker_pool_wait_count_delta, $r.mysql_threads_running_peak, $r.buffer_pool_hit_rate))
}
[void]$sb.AppendLine("")
[void]$sb.AppendLine("## Top SQL digests per rate")
foreach ($rate in $rateList) {
  $digestPath = Join-Path $outputPath ("rate-" + $rate + "\digest-top.json")
  [void]$sb.AppendLine("")
  [void]$sb.AppendLine("### rate=$rate")
  if (Test-Path $digestPath) {
    $tops = Get-Content $digestPath -Raw -Encoding utf8 | ConvertFrom-Json
    [void]$sb.AppendLine("| total_s | count | avg_ms | max_ms | rows_ex | digest |")
    [void]$sb.AppendLine("| ---: | ---: | ---: | ---: | ---: | --- |")
    foreach ($t in @($tops | Select-Object -First 12)) {
      $d = ([string]$t.digest) -replace '\s+', ' '
      if ($d.Length -gt 120) { $d = $d.Substring(0, 120) + "..." }
      [void]$sb.AppendLine(("| {0} | {1} | {2} | {3} | {4} | `{5}` |" -f $t.total_s, $t.count, $t.avg_ms, $t.max_ms, $t.rows_examined, $d))
    }
  }
}
[void]$sb.AppendLine("")
[void]$sb.AppendLine("## Reading guide")
[void]$sb.AppendLine("- If wait_count/wait_dur jump 600->800 while in_use~max_open: HTTP and Consumer share pool and queue on connections.")
[void]$sb.AppendLine("- If thr_running high with low wait_count: MySQL compute/lock saturation more than Go pool.")
[void]$sb.AppendLine("- Digest total_s leaders among SELECT event/order show which read APIs burn DB time.")

$mdText = $sb.ToString()
[System.IO.File]::WriteAllText((Join-Path $outputPath "RESOURCE_PROFILE.md"), $mdText, [System.Text.UTF8Encoding]::new($false))
Write-Host $mdText
Write-Host ("Profile: " + (Join-Path $outputPath "RESOURCE_PROFILE.md")) -ForegroundColor Green

if (-not $KeepStack) {
  Invoke-Compose @("down", "-v", "--remove-orphans")
}
