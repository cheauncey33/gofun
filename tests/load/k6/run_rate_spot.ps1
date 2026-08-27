param(
  [int]$Rate = 200,
  [string]$Duration = "60s",
  [int]$ConsumerWorkers = 8,
  [int]$InventoryBucketCount = 32,
  [int]$OutboxPublishWorkers = 4,
  [int]$PostSampleSeconds = 30,
  [int]$InnoDBFlushLogAtTrxCommit = 1,
  [switch]$SkipLogBin,
  [string]$Project = "gofun-csweep",
  [string]$BackendPort = "18590",
  [string]$ElasticsearchPort = "19610",
  [string]$MysqlPort = "13330",
  [string]$RedisPort = "16410",
  [string]$RabbitPort = "26080",
  [string]$RabbitManagementPort = "36080",
  [string]$OutputDir = "tests/load/results/rate-spot-$Rate-rps-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [switch]$ReuseStack,
  [switch]$KeepStack,
  [switch]$FastPrepare,
  [switch]$K6InDocker,
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

$composeArgs = @("compose", "-p", $Project, "-f", $baseCompose, "-f", $capacityCompose)
if ($SkipLogBin) { $composeArgs += @("-f", $nobinlogCompose) }

function Invoke-Compose([string[]]$Arguments) {
  & docker @composeArgs @Arguments
  if ($LASTEXITCODE -ne 0) { throw "docker compose failed: $LASTEXITCODE" }
}

function Wait-ForBackend {
  $healthUrl = "http://127.0.0.1:$BackendPort/healthz"
  $deadline = [DateTime]::UtcNow.AddSeconds(180)
  do {
    $state = (docker inspect -f "{{.State.Status}}|{{.State.ExitCode}}" $backendContainer 2>$null)
    if ($state -match '^exited\|') {
      cmd /c "docker logs --tail 40 $backendContainer 2>&1"
      throw "backend exited during startup: $state"
    }
    try {
      $response = Invoke-WebRequest -UseBasicParsing -Uri $healthUrl -TimeoutSec 3
      if ($response.StatusCode -eq 200) { return }
    } catch {}
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  throw "backend health check timeout: $healthUrl"
}

function Set-CommonEnv {
  $env:CAPACITY_CONTAINER_PREFIX = $Project
  $env:GOFUN_BACKEND_PORT = $BackendPort
  $env:GOFUN_ELASTICSEARCH_PORT = $ElasticsearchPort
  $env:GOFUN_MYSQL_PORT = $MysqlPort
  $env:GOFUN_REDIS_PORT = $RedisPort
  $env:GOFUN_RABBITMQ_PORT = $RabbitPort
  $env:GOFUN_RABBITMQ_MANAGEMENT_PORT = $RabbitManagementPort
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
  $env:MYSQL_MAX_OPEN_CONNS = "100"
  $env:MYSQL_MAX_IDLE_CONNS = "10"
  $env:REDIS_POOL_SIZE = "100"
  $env:RATELIMIT_DISTRIBUTED_WRITE_ENABLED = "true"
  $env:RABBITMQ_QUEUE_TYPE = "classic"
  $env:MYSQL_CONTAINER = $mysqlContainer
  $env:K6_JWT_SECRET = "fuchang-integration-test-secret-only"
  $env:TELEMETRY_ENABLED = "false"
}

function Invoke-DbQuery([string]$Query) {
  @(docker exec -e MYSQL_PWD=fuchang-it-mysql $mysqlContainer mysql -ufuchang -N -B fuchang_ticketing_it -e $Query 2>$null)
}

function Clear-Residue {
  Write-Host "Clearing residue..." -ForegroundColor DarkGray
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
  if ($LASTEXITCODE -ne 0) { throw "truncate failed" }
  docker exec $redisContainer redis-cli FLUSHDB 2>$null | Out-Null
  foreach ($q in @("fuchang.it.order.queue","fuchang.it.order.retry","fuchang.it.order.dead","fuchang.order.delay","fuchang.order.timeout")) {
    docker exec $rabbitContainer rabbitmqctl purge_queue $q 2>$null | Out-Null
  }
}

function Get-RabbitSnapshot {
  $rows = @(docker exec $rabbitContainer rabbitmqctl list_queues name messages_ready messages_unacknowledged --quiet 2>$null)
  $ready = 0L; $unacked = 0L; $orderReady = 0L; $orderUnacked = 0L
  foreach ($row in $rows) {
    $parts = @(($row -split "\s+") | Where-Object { $_ -ne "" })
    if ($parts.Count -ge 3 -and $parts[1] -match '^\d+$' -and $parts[2] -match '^\d+$') {
      $ready += [int64]$parts[1]
      $unacked += [int64]$parts[2]
      if ($parts[0] -eq "fuchang.it.order.queue") {
        $orderReady = [int64]$parts[1]
        $orderUnacked = [int64]$parts[2]
      }
    }
  }
  [pscustomobject]@{
    messages_ready = $ready
    messages_unacknowledged = $unacked
    order_ready = $orderReady
    order_unacked = $orderUnacked
    work_queue_total = $orderReady + $orderUnacked
  }
}

function Get-DbSnapshot([string]$CampaignId) {
  $orders = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order WHERE rush_sale_campaign_id=$CampaignId;")
  $pendingPayment = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order WHERE rush_sale_campaign_id=$CampaignId AND status='pending_payment';")
  $queued = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order WHERE rush_sale_campaign_id=$CampaignId AND status='queued';")
  $pendingOutbox = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order_outbox o JOIN ticket_order t ON t.id=o.order_id WHERE t.rush_sale_campaign_id=$CampaignId AND o.status IN ('pending','publishing');")
  [pscustomobject]@{
    orders = if ($orders.Count) { [int64]$orders[0] } else { 0 }
    pending_payment = if ($pendingPayment.Count) { [int64]$pendingPayment[0] } else { 0 }
    queued = if ($queued.Count) { [int64]$queued[0] } else { 0 }
    pending_outbox = if ($pendingOutbox.Count) { [int64]$pendingOutbox[0] } else { 0 }
  }
}

function Get-PrometheusSnapshot {
  try {
    $body = (Invoke-WebRequest -UseBasicParsing -Uri $metricsUrl -TimeoutSec 5).Content
    $result = [ordered]@{}
    foreach ($line in ($body -split "`n")) {
      if ($line -match '^(?<name>[a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(?<labels>[^}]*)\})?\s+(?<value>[-+]?[0-9]*\.?[0-9]+(?:[eE][-+]?[0-9]+)?)') {
        $key = if ([string]::IsNullOrWhiteSpace($Matches.labels)) { $Matches.name } else { "$($Matches.name){$($Matches.labels)}" }
        $result[$key] = [double]$Matches.value
      }
    }
    return [pscustomobject]$result
  } catch { return [pscustomobject]@{} }
}

function Get-ConsumerTxTotal([object]$Prom) {
  $tx = 0.0
  if ($null -eq $Prom) { return $tx }
  foreach ($p in $Prom.PSObject.Properties) {
    if ($p.Name -like 'ticket_order_consumer_transactions_total*') { $tx += [double]$p.Value }
  }
  return $tx
}

function Get-Sample([string]$Phase, [datetime]$Started, [string]$CampaignId) {
  $prom = Get-PrometheusSnapshot
  [pscustomobject]@{
    timestamp = (Get-Date).ToString("o")
    phase = $Phase
    elapsed_seconds = [Math]::Round(((Get-Date) - $Started).TotalSeconds, 1)
    rabbitmq = Get-RabbitSnapshot
    db = Get-DbSnapshot $CampaignId
    prometheus = $prom
    consumer_tx_total = Get-ConsumerTxTotal $prom
  }
}

Set-CommonEnv
if (-not $ReuseStack) {
  Invoke-Compose @("up", "-d", "--build", "--wait")
} else {
  # Ensure backend workers match requested config.
  Invoke-Compose @("up", "-d", "--no-deps", "--force-recreate", "backend")
}
Wait-ForBackend
docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit;" 2>$null | Out-Null
$dur = @(docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SHOW VARIABLES WHERE Variable_name IN ('innodb_flush_log_at_trx_commit','log_bin','sync_binlog');" 2>$null)
Write-Host ("MySQL durability: " + ($dur -join " | ")) -ForegroundColor DarkYellow
Write-Host ("Config: rate=${Rate}/s duration=$Duration consumers=$ConsumerWorkers buckets=$InventoryBucketCount") -ForegroundColor Cyan

Clear-Residue
Invoke-Compose @("up", "-d", "--no-deps", "--force-recreate", "backend")
Wait-ForBackend
docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit;" 2>$null | Out-Null

$env:BASE_URL = $baseUrl
$env:K6_USERS = "3000"
$env:RUSH_TOTAL_QUOTA = "200000"
$env:RUSH_PER_USER_LIMIT = "20"
$env:SETUP_CONCURRENCY = "100"
$env:RUSH_LABEL = "rate-spot-$Rate-$(Get-Date -Format yyyyMMdd-HHmmss)"
$prepareScript = if ($FastPrepare) {
  Join-Path $repo "tests\load\k6\prepare_rush_fixture_fast.mjs"
} else {
  Join-Path $repo "tests\load\k6\prepare_rush_fixture.mjs"
}
& node $prepareScript
if ($LASTEXITCODE -ne 0) { throw "fixture preparation failed" }
$fixture = Get-Content (Join-Path $repo "tests\load\k6\fixtures\rush_execute.json") -Raw -Encoding utf8 | ConvertFrom-Json
$campaignId = [string]$fixture.campaign_id

$samples = [System.Collections.Generic.List[object]]::new()
$started = Get-Date
$samples.Add((Get-Sample "pre_http" $started $campaignId))

$summaryPath = Join-Path $outputPath "k6-summary.json"
$k6Stdout = Join-Path $outputPath "k6.stdout.log"
$k6Stderr = Join-Path $outputPath "k6.stderr.log"
$k6Root = Join-Path $repo "tests\load\k6"
$preVUs = [Math]::Max($Rate, 50)
$maxVUs = [Math]::Max($Rate * 2, $preVUs)

if ($K6InDocker) {
  $k6Args = @(
    "run", "--rm", "--network", $k6Network, "--workdir", "/work",
    "--volume", "${k6Root}:/work",
    "--volume", "${outputPath}:/results",
    "--env", "BASE_URL=$k6BaseUrl",
    "--env", "K6_SUMMARY=/results/k6-summary.json",
    "--env", "RATE=$Rate",
    "--env", "DURATION=$Duration",
    "--env", "PRE_VUS=$preVUs",
    "--env", "MAX_VUS=$maxVUs",
    $K6Image, "run", "/work/rush_execute.js"
  )
  $process = Start-Process -FilePath "docker" -ArgumentList $k6Args -WorkingDirectory $repo -NoNewWindow -PassThru `
    -RedirectStandardOutput $k6Stdout -RedirectStandardError $k6Stderr
} else {
  throw "host k6 path not wired; use -K6InDocker"
}

while (-not $process.HasExited) {
  $samples.Add((Get-Sample "http" $started $campaignId))
  Start-Sleep -Seconds 1
  $process.Refresh()
}
$process.Refresh()
$httpEnd = Get-Date

$postDeadline = (Get-Date).AddSeconds($PostSampleSeconds)
while ((Get-Date) -lt $postDeadline) {
  $samples.Add((Get-Sample "post_http" $started $campaignId))
  Start-Sleep -Seconds 1
}

$samples | ConvertTo-Json -Depth 12 | Set-Content -Encoding utf8 (Join-Path $outputPath "lifecycle-samples.json")
& node (Join-Path $repo "tests\load\analyze_consumer_tx_stages.mjs") (Join-Path $outputPath "lifecycle-samples.json") |
  Out-File -Encoding utf8 (Join-Path $outputPath "stage-percentiles.json")

$k6 = if (Test-Path $summaryPath) {
  Get-Content $summaryPath -Raw -Encoding utf8 | ConvertFrom-Json
} else { $null }
$stages = if (Test-Path (Join-Path $outputPath "stage-percentiles.json")) {
  Get-Content (Join-Path $outputPath "stage-percentiles.json") -Raw -Encoding utf8 | ConvertFrom-Json
} else { $null }

$httpSamples = @($samples | Where-Object { $_.phase -eq "http" })
$firstHttp = $httpSamples[0]
$lastHttp = $httpSamples[-1]
$lastAll = $samples[-1]
$httpDt = [Math]::Max(0.001, $lastHttp.elapsed_seconds - $firstHttp.elapsed_seconds)
$consumerDeltaHttp = $lastHttp.consumer_tx_total - $firstHttp.consumer_tx_total
$consumerRateHttp = [Math]::Round($consumerDeltaHttp / $httpDt, 1)
$ppDeltaHttp = $lastHttp.db.pending_payment - $firstHttp.db.pending_payment
$ppDeltaAll = $lastAll.db.pending_payment - $samples[0].db.pending_payment

$readySeries = @($samples | ForEach-Object {
  [pscustomobject]@{ t = $_.elapsed_seconds; phase = $_.phase; ready = $_.rabbitmq.order_ready; outbox = $_.db.pending_outbox; pp = $_.db.pending_payment; tx = $_.consumer_tx_total }
})
$readyPeak = ($readySeries | Measure-Object -Property ready -Maximum).Maximum
$outboxPeak = ($readySeries | Measure-Object -Property outbox -Maximum).Maximum

$txStage = $null
$commitStage = $null
if ($stages -and $stages.stages) {
  foreach ($s in $stages.stages) {
    $name = [string]$s.name
    if ($name -eq "stage=commit" -or $name -eq "commit") { $commitStage = $s }
  }
  $txStage = $stages.whole_transaction
}

$report = [ordered]@{
  config = [ordered]@{
    target_rate = $Rate
    duration = $Duration
    consumer_workers = $ConsumerWorkers
    bucket_count = $InventoryBucketCount
    outbox_publish_workers = $OutboxPublishWorkers
    skip_log_bin = [bool]$SkipLogBin
    flush_log = $InnoDBFlushLogAtTrxCommit
    campaign_id = $campaignId
  }
  http = [ordered]@{
    achieved_req_rate = $k6.http_req_rate
    http_reqs = $k6.http_reqs
    success_rate = $k6.rush_execute_success_rate
    p50_ms = $k6.p50_ms
    p95_ms = $k6.p95_ms
    p99_ms = $k6.p99_ms
  }
  consumer = [ordered]@{
    complete_rate_during_http = $consumerRateHttp
    tx_delta_during_http = [Math]::Round($consumerDeltaHttp, 0)
    tx_total_end = [Math]::Round($lastAll.consumer_tx_total, 0)
    tx_p95_ms = $txStage.p95_ms
    tx_p99_ms = $txStage.p99_ms
    commit_p95_ms = $commitStage.p95_ms
    commit_p99_ms = $commitStage.p99_ms
  }
  queues = [ordered]@{
    order_ready_peak = $readyPeak
    order_ready_end_http = $lastHttp.rabbitmq.order_ready
    order_ready_end = $lastAll.rabbitmq.order_ready
    outbox_pending_peak = $outboxPeak
    outbox_pending_end_http = $lastHttp.db.pending_outbox
    outbox_pending_end = $lastAll.db.pending_outbox
  }
  pending_payment = [ordered]@{
    start = $samples[0].db.pending_payment
    end_http = $lastHttp.db.pending_payment
    end = $lastAll.db.pending_payment
    delta_during_http = $ppDeltaHttp
    delta_total = $ppDeltaAll
  }
  k6_exit_code = $process.ExitCode
  http_wall_seconds = [Math]::Round(($httpEnd - $started).TotalSeconds, 1)
}

$report | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $outputPath "rate-spot-summary.json")

$md = @"
# Rate spot: ${Rate} req/s x $Duration

固定：Consumer=$ConsumerWorkers, bucket=$InventoryBucketCount, outbox=$OutboxPublishWorkers, prefetch=5

| 指标 | 值 |
| --- | ---: |
| HTTP 输入速率 (achieved) | $([Math]::Round([double]$k6.http_req_rate, 1)) req/s |
| HTTP 总请求 | $($k6.http_reqs) |
| HTTP 成功率 | $($k6.rush_execute_success_rate) |
| HTTP P95 | $([Math]::Round([double]$k6.p95_ms, 1)) ms |
| HTTP P99 | $([Math]::Round([double]$k6.p99_ms, 1)) ms |
| Consumer 完成速率 (HTTP 窗) | $consumerRateHttp tx/s |
| Consumer tx P95 | $($txStage.p95_ms) ms |
| Consumer tx P99 | $($txStage.p99_ms) ms |
| RabbitMQ order ready peak | $readyPeak |
| RabbitMQ order ready @HTTP end | $($lastHttp.rabbitmq.order_ready) |
| Outbox pending peak | $outboxPeak |
| Outbox pending @HTTP end | $($lastHttp.db.pending_outbox) |
| pending_payment Δ (HTTP 窗) | $ppDeltaHttp |
| pending_payment end | $($lastAll.db.pending_payment) |
"@
$md | Set-Content -Encoding utf8 (Join-Path $outputPath "rate-spot-summary.md")
Write-Host $md
Write-Host ("Summary: " + (Join-Path $outputPath "rate-spot-summary.md")) -ForegroundColor Green

if (-not $KeepStack -and -not $ReuseStack) {
  Invoke-Compose @("down", "-v", "--remove-orphans")
}
