param(
  [string]$Project = "gofun-csweep",
  [string]$Consumers = "4,6,8,10,12,14,16",
  [int]$Vus = 500,
  [string]$Duration = "20s",
  [int]$DrainSeconds = 90,
  [int]$StabilizeMinSeconds = 24,
  [int]$StabilizeWindowSeconds = 16,
  [double]$StabilizeRelTol = 0.12,
  [int]$InventoryBucketCount = 32,
  [int]$OutboxPublishWorkers = 4,
  [int]$PaymentTimeoutWorkers = 2,
  [int]$InnoDBFlushLogAtTrxCommit = 1,
  [switch]$SkipLogBin,
  [string]$BackendPort = "18590",
  [string]$ElasticsearchPort = "19610",
  [string]$MysqlPort = "13330",
  [string]$RedisPort = "16410",
  [string]$RabbitPort = "26080",
  [string]$RabbitManagementPort = "36080",
  [string]$OutputDir = "tests/load/results/consumer-sweep-nobinlog-$(Get-Date -Format yyyyMMdd-HHmmss)",
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
$baseUrl = "http://127.0.0.1:$BackendPort/api/v1"
$metricsUrl = "http://127.0.0.1:$BackendPort/metrics"
$k6Network = "${Project}_default"
$k6BaseUrl = "http://backend:8080/api/v1"
$consumerList = @($Consumers.Split(",") | ForEach-Object { [int]$_.Trim() } | Where-Object { $_ -gt 0 })
if ($consumerList.Count -eq 0) { throw "Consumers must contain positive integers" }

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
      if ($response.StatusCode -eq 200) {
        Write-Host "Backend ready: $healthUrl" -ForegroundColor DarkGreen
        return
      }
    } catch {}
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  cmd /c "docker logs --tail 40 $backendContainer 2>&1"
  throw "backend health check timeout: $healthUrl"
}

function Clear-SweepResidue {
  # Keep A/B points independent: previous campaigns' orders must not bloat warm-up
  # or distort Redis user-count keys / MySQL lock stats.
  Write-Host "Clearing order residue before next consumer point..." -ForegroundColor DarkGray
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
  $truncateOut = cmd /c "docker exec -e MYSQL_PWD=fuchang-it-mysql $mysqlContainer mysql -ufuchang -N -B fuchang_ticketing_it -e `"$sql`" 2>&1"
  if ($LASTEXITCODE -ne 0) {
    Write-Host $truncateOut
    throw "Clear-SweepResidue MySQL truncate failed: $LASTEXITCODE"
  }
  docker exec "${Project}-redis-1" redis-cli FLUSHDB 2>$null | Out-Null
  Clear-OrderQueues
}

function Clear-OrderQueues {
  foreach ($q in @(
    "fuchang.it.order.queue",
    "fuchang.it.order.retry",
    "fuchang.it.order.dead",
    "fuchang.order.delay",
    "fuchang.order.timeout"
  )) {
    docker exec $rabbitContainer rabbitmqctl purge_queue $q 2>$null | Out-Null
  }
}

function Get-ConsumerTxTotal([object]$Prom) {
  $tx = 0.0
  if ($null -eq $Prom) { return $tx }
  foreach ($p in $Prom.PSObject.Properties) {
    if ($p.Name -like 'ticket_order_consumer_transactions_total*') {
      $tx += [double]$p.Value
    }
  }
  return $tx
}

function Get-ConsumerBusyThroughput([string]$LifecyclePath) {
  if (-not (Test-Path $LifecyclePath)) {
    return [pscustomobject]@{ consumer_tx_per_sec = 0; consumer_tx_total = 0; busy_seconds = 0 }
  }
  $raw = Get-Content $LifecyclePath -Raw -Encoding utf8
  if ($raw.Length -gt 0 -and [int][char]$raw[0] -eq 0xFEFF) { $raw = $raw.Substring(1) }
  $life = $raw | ConvertFrom-Json
  $httpEnd = 0.0
  $lastPhase = ""
  $absBase = 0.0
  $points = New-Object System.Collections.Generic.List[object]
  foreach ($s in @($life)) {
    $phase = [string]$s.phase
    if ($phase -eq "drain" -and $lastPhase -ne "drain") { $absBase = $httpEnd }
    $t = if ($phase -eq "drain") { $absBase + [double]$s.elapsed_seconds } else { [double]$s.elapsed_seconds }
    if ($phase -ne "drain") { $httpEnd = [Math]::Max($httpEnd, [double]$s.elapsed_seconds) }
    $lastPhase = $phase
    $tx = 0.0
    if ($null -ne $s.prometheus) {
      foreach ($p in $s.prometheus.PSObject.Properties) {
        if ($p.Name -like 'ticket_order_consumer_transactions_total*') {
          $tx += [double]$p.Value
        }
      }
    }
    $points.Add([pscustomobject]@{ t = $t; tx = $tx })
  }
  $withTx = @($points | Where-Object { $_.tx -gt 0 } | Sort-Object t)
  if ($withTx.Count -lt 2) {
    return [pscustomobject]@{ consumer_tx_per_sec = 0; consumer_tx_total = 0; busy_seconds = 0 }
  }
  $start = $withTx[0]
  $maxTx = ($withTx | Measure-Object -Property tx -Maximum).Maximum
  $firstMax = @($withTx | Where-Object { $_.tx -eq $maxTx })[0]
  $busy = [Math]::Max(0.001, $firstMax.t - $start.t)
  $rate = [Math]::Round(($maxTx - $start.tx) / $busy, 1)
  return [pscustomobject]@{
    consumer_tx_per_sec = $rate
    consumer_tx_total = [int64]$maxTx
    busy_seconds = [Math]::Round($busy, 1)
  }
}

function Invoke-DbQuery([string]$Query) {
  @(docker exec -e MYSQL_PWD=fuchang-it-mysql $mysqlContainer mysql -ufuchang -N -B fuchang_ticketing_it -e $Query 2>$null)
}

function Get-RabbitSnapshot {
  $rows = @(docker exec $rabbitContainer rabbitmqctl list_queues name messages_ready messages_unacknowledged --quiet 2>$null)
  $queueTotals = [ordered]@{}
  foreach ($row in $rows) {
    $parts = ($row -split "\s+") | Where-Object { $_ -ne "" }
    if ($parts.Count -ge 3 -and $parts[1] -match '^\d+$' -and $parts[2] -match '^\d+$') {
      $queueTotals[$parts[0]] = [pscustomobject]@{
        ready = [int64]$parts[1]
        unacknowledged = [int64]$parts[2]
        total = [int64]$parts[1] + [int64]$parts[2]
      }
    }
  }
  $order = if ($queueTotals.Contains("fuchang.it.order.queue")) { $queueTotals["fuchang.it.order.queue"] } else { [pscustomobject]@{ ready = 0; unacknowledged = 0; total = 0 } }
  $retry = if ($queueTotals.Contains("fuchang.it.order.retry")) { $queueTotals["fuchang.it.order.retry"] } else { [pscustomobject]@{ ready = 0; unacknowledged = 0; total = 0 } }
  $dead = if ($queueTotals.Contains("fuchang.it.order.dead")) { $queueTotals["fuchang.it.order.dead"] } else { [pscustomobject]@{ ready = 0; unacknowledged = 0; total = 0 } }
  $delay = if ($queueTotals.Contains("fuchang.order.delay")) { $queueTotals["fuchang.order.delay"] } else { [pscustomobject]@{ ready = 0; unacknowledged = 0; total = 0 } }
  $timeout = if ($queueTotals.Contains("fuchang.order.timeout")) { $queueTotals["fuchang.order.timeout"] } else { [pscustomobject]@{ ready = 0; unacknowledged = 0; total = 0 } }
  [pscustomobject]@{
    work_queue_total = $order.total + $retry.total
    dead_letters = $dead.total
    primary_total_messages = $order.total + $delay.total + $timeout.total + $retry.total
    total_messages = ($queueTotals.Values | ForEach-Object { $_.total } | Measure-Object -Sum).Sum
    order_queue = $order.total
    retry_queue = $retry.total
  }
}

function Get-DbSnapshot([string]$CampaignId) {
  $orderCount = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order WHERE rush_sale_campaign_id = $CampaignId;")
  $pendingOutbox = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order_outbox o JOIN ticket_order t ON t.id = o.order_id WHERE t.rush_sale_campaign_id = $CampaignId AND o.status IN ('pending','publishing');")
  [pscustomobject]@{
    orders = if ($orderCount.Count -gt 0) { [int64]$orderCount[0] } else { 0 }
    pending_outbox = if ($pendingOutbox.Count -gt 0) { [int64]$pendingOutbox[0] } else { 0 }
  }
}

function Get-PrometheusSnapshot {
  try {
    $body = (Invoke-WebRequest -UseBasicParsing -Uri $metricsUrl -TimeoutSec 5).Content
    $result = [ordered]@{}
    foreach ($line in ($body -split "`n")) {
      if ($line -match '^(?<name>[a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(?<labels>[^}]*)\})?\s+(?<value>[-+]?[0-9]*\.?[0-9]+(?:[eE][-+]?[0-9]+)?)') {
        $name = $Matches.name
        $labels = $Matches.labels
        $key = if ([string]::IsNullOrWhiteSpace($labels)) { $name } else { "$name{$labels}" }
        $result[$key] = [double]$Matches.value
      }
    }
    return [pscustomobject]$result
  } catch {
    return [pscustomobject]@{}
  }
}

function Get-Maximum([object[]]$Values) {
  $numbers = @($Values | Where-Object { $_ -ne $null } | ForEach-Object { [double]$_ })
  if ($numbers.Count -eq 0) { return 0 }
  return ($numbers | Measure-Object -Maximum).Maximum
}

function Wait-ForStableConsume([string]$CampaignId, [string]$RunPath) {
  # Do not wait for full drain. Observe consumer tx/s until the recent window is
  # stable (or backlog is gone), then purge leftover MQ messages for the next point.
  $drainSamples = [System.Collections.Generic.List[object]]::new()
  $rates = [System.Collections.Generic.List[double]]::new()
  $started = Get-Date
  $prevTx = $null
  $prevAt = $null
  $stable = $false
  $reason = "max_observe"
  $sampleIntervalSec = 2

  while (((Get-Date) - $started).TotalSeconds -lt $DrainSeconds) {
    $rabbit = Get-RabbitSnapshot
    $db = Get-DbSnapshot $CampaignId
    $prom = Get-PrometheusSnapshot
    $tx = Get-ConsumerTxTotal $prom
    $elapsed = [Math]::Round(((Get-Date) - $started).TotalSeconds, 1)
    $instRate = $null
    if ($null -ne $prevTx -and $null -ne $prevAt) {
      $dt = [Math]::Max(0.001, ((Get-Date) - $prevAt).TotalSeconds)
      $instRate = [Math]::Round(($tx - $prevTx) / $dt, 1)
      if ($instRate -lt 0) { $instRate = 0 }
      $rates.Add([double]$instRate)
    }
    $prevTx = $tx
    $prevAt = Get-Date

    $sample = [pscustomobject]@{
      timestamp = (Get-Date).ToString("o")
      phase = "stabilize"
      elapsed_seconds = $elapsed
      rabbitmq = $rabbit
      prometheus = $prom
      db = $db
      consumer_tx_total = $tx
      instant_tx_per_sec = $instRate
    }
    $drainSamples.Add($sample)

    if ($rabbit.work_queue_total -eq 0 -and $db.pending_outbox -eq 0) {
      $reason = "queue_empty"
      $stable = $true
      break
    }

    if ($elapsed -ge $StabilizeMinSeconds -and $rates.Count -ge 3) {
      $windowCount = [Math]::Max(2, [int][Math]::Ceiling($StabilizeWindowSeconds / $sampleIntervalSec))
      if ($rates.Count -ge $windowCount) {
        $window = @($rates | Select-Object -Last $windowCount)
        $mean = ($window | Measure-Object -Average).Average
        $min = ($window | Measure-Object -Minimum).Minimum
        $max = ($window | Measure-Object -Maximum).Maximum
        $rel = if ($mean -gt 1) { ($max - $min) / $mean } else { 1 }
        # Require meaningful consumption and backlog still present so we measure under load.
        if ($mean -ge 20 -and $rel -le $StabilizeRelTol -and $rabbit.work_queue_total -gt 0) {
          $reason = "rate_stable"
          $stable = $true
          break
        }
      }
    }
    Start-Sleep -Seconds $sampleIntervalSec
  }

  $purgedReady = 0
  if ($drainSamples.Count -gt 0) {
    $purgedReady = [int64]$drainSamples[-1].rabbitmq.work_queue_total
  }
  Write-Host ("stabilize done: reason={0} elapsed={1}s leftover_wq={2} -> purge" -f $reason, $(if ($drainSamples.Count -gt 0) { $drainSamples[-1].elapsed_seconds } else { 0 }), $purgedReady) -ForegroundColor DarkYellow
  Clear-OrderQueues

  $stableRate = 0.0
  $windowCount = [Math]::Max(2, [int][Math]::Ceiling($StabilizeWindowSeconds / $sampleIntervalSec))
  if ($rates.Count -gt 0) {
    $window = @($rates | Select-Object -Last ([Math]::Min($windowCount, $rates.Count)))
    $stableRate = [Math]::Round(($window | Measure-Object -Average).Average, 1)
  }

  $drainSamples | ConvertTo-Json -Depth 20 | Set-Content -Encoding utf8 (Join-Path $RunPath "drain-samples.json")
  [pscustomobject]@{
    elapsed_seconds = if ($drainSamples.Count -gt 0) { $drainSamples[-1].elapsed_seconds } else { 0 }
    primary_drained = ($reason -eq "queue_empty")
    completed = $false
    truncated = ($reason -ne "queue_empty")
    stabilize_reason = $reason
    stable = $stable
    stable_tx_per_sec = $stableRate
    purged_work_queue = $purgedReady
    max_work_queue_total = Get-Maximum @($drainSamples | ForEach-Object { $_.rabbitmq.work_queue_total })
    max_rabbit_total = Get-Maximum @($drainSamples | ForEach-Object { $_.rabbitmq.total_messages })
    last_rabbit = if ($drainSamples.Count -gt 0) { $drainSamples[-1].rabbitmq } else { Get-RabbitSnapshot }
    last_db = if ($drainSamples.Count -gt 0) { $drainSamples[-1].db } else { Get-DbSnapshot $CampaignId }
  }
}

function Set-CommonEnv([int]$Workers) {
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
  $env:ORDER_CONSUMER_WORKER_COUNT = [string]$Workers
  $env:ORDER_CONSUMER_PREFETCH_COUNT = "5"
  $env:ORDER_CONSUMER_MAX_RETRIES = "3"
  $env:DELAYED_ORDER_WORKER_COUNT = [string]$PaymentTimeoutWorkers
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

# Free competing diagnostic stacks so A/B is not IO-contaminated.
foreach ($p in @("gofun-txprofile", "gofun-1plus0", "gofun-nobinlog")) {
  Write-Host "Stopping competing stack: $p" -ForegroundColor DarkGray
  cmd /c "docker compose -p $p -f `"$baseCompose`" -f `"$capacityCompose`" down -v --remove-orphans >NUL 2>&1"
}

$rows = @()
try {
  Set-CommonEnv $consumerList[0]
  Invoke-Compose @("down", "-v", "--remove-orphans")
  Invoke-Compose @("up", "-d", "--build", "--wait")
  Wait-ForBackend
  docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit;" 2>$null | Out-Null
  $dur = @(docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SHOW VARIABLES WHERE Variable_name IN ('innodb_flush_log_at_trx_commit','log_bin','sync_binlog');" 2>$null)
  Write-Host ("MySQL durability: " + ($dur -join " | ")) -ForegroundColor DarkYellow
  if ($SkipLogBin -and -not ($dur -match 'log_bin\s+OFF')) {
    throw "SkipLogBin requested but log_bin is not OFF"
  }

  foreach ($workers in $consumerList) {
    Write-Host "=== consumer workers=$workers ===" -ForegroundColor Cyan
    Set-CommonEnv $workers
    Clear-SweepResidue
    Invoke-Compose @("up", "-d", "--no-deps", "--force-recreate", "backend")
    Wait-ForBackend
    # Re-apply durability knobs after backend/mysql churn; skip-log-bin stack keeps log_bin OFF.
    docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit;" 2>$null | Out-Null
    Start-Sleep -Seconds 2

    $runName = "consumer-$workers"
    $runDirRelative = Join-Path $OutputDir $runName
    $runDir = Join-Path $repo $runDirRelative
    New-Item -ItemType Directory -Force -Path $runDir | Out-Null

    $env:BASE_URL = $baseUrl
    $env:K6_USERS = "3000"
    $env:RUSH_TOTAL_QUOTA = "200000"
    $env:RUSH_PER_USER_LIMIT = "20"
    $env:SETUP_CONCURRENCY = "100"
    $env:RUSH_LABEL = "csweep-c$workers-$(Get-Date -Format yyyyMMdd-HHmmss)"
    Remove-Item Env:K6_ALLOW_HIGH_PER_USER_LIMIT -ErrorAction SilentlyContinue

    $prepareScript = if ($FastPrepare) {
      Join-Path $repo "tests\load\k6\prepare_rush_fixture_fast.mjs"
    } else {
      Join-Path $repo "tests\load\k6\prepare_rush_fixture.mjs"
    }
    & node $prepareScript
    if ($LASTEXITCODE -ne 0) { throw "fixture preparation failed for consumer=$workers" }
    $fixture = Get-Content (Join-Path $repo "tests\load\k6\fixtures\rush_execute.json") -Raw -Encoding utf8 | ConvertFrom-Json
    $campaignId = [string]$fixture.campaign_id

    $diag = Join-Path $repo "tests\load\k6\run_peak_diagnostic.ps1"
    $diagArgs = @(
      "-Vus", $Vus,
      "-Duration", $Duration,
      "-BaseUrl", $(if ($K6InDocker) { $k6BaseUrl } else { $baseUrl }),
      "-MetricsUrl", $metricsUrl,
      "-RabbitContainer", $rabbitContainer,
      "-MysqlContainer", $mysqlContainer,
      "-OutputDir", $runDirRelative
    )
    if ($K6InDocker) {
      $diagArgs += @("-K6InDocker", "-K6Network", $k6Network, "-K6Image", $K6Image)
    }
    & powershell -NoProfile -ExecutionPolicy Bypass -File $diag @diagArgs
    if ($LASTEXITCODE -ne 0) { throw "diagnostic failed for consumer=$workers" }

    $diagnostic = Get-Content (Join-Path $runDir "diagnostic-summary.json") -Raw -Encoding utf8 | ConvertFrom-Json
    $k6 = $diagnostic.summary
    $drain = Wait-ForStableConsume $campaignId $runDir

    $httpSamples = Get-Content (Join-Path $runDir "samples.json") -Raw -Encoding utf8 | ConvertFrom-Json
    $drainSamples = Get-Content (Join-Path $runDir "drain-samples.json") -Raw -Encoding utf8 | ConvertFrom-Json
    $lifecycle = [System.Collections.Generic.List[object]]::new()
    foreach ($s in @($httpSamples)) { $lifecycle.Add($s) }
    foreach ($s in @($drainSamples)) { $lifecycle.Add($s) }
    $lifecycle | ConvertTo-Json -Depth 20 | Set-Content -Encoding utf8 (Join-Path $runDir "lifecycle-samples.json")

    $orders = $drain.last_db.orders
    $ordersPerSecLegacy = if ($drain.elapsed_seconds -gt 0) { [Math]::Round($orders / $drain.elapsed_seconds, 1) } else { 0 }
    $busy = Get-ConsumerBusyThroughput (Join-Path $runDir "lifecycle-samples.json")
    # Prefer post-k6 stable-window rate; fall back to full busy-window average.
    $txPerSec = if ($drain.stable_tx_per_sec -gt 0) { $drain.stable_tx_per_sec } else { $busy.consumer_tx_per_sec }
    $row = [pscustomobject]@{
      consumer_workers = $workers
      bucket_count = $InventoryBucketCount
      skip_log_bin = [bool]$SkipLogBin
      flush_log = $InnoDBFlushLogAtTrxCommit
      vus = $Vus
      http_req_rate = $k6.http_req_rate
      http_reqs = $k6.http_reqs
      http_success_rate = $k6.rush_execute_success_rate
      p99_ms = $k6.p99_ms
      http_window_work_queue_peak = $diagnostic.peaks.rabbitmq_work_queue_total
      post_test_work_queue_peak = $drain.max_work_queue_total
      drain_seconds = $drain.elapsed_seconds
      primary_drained = $drain.primary_drained
      truncated = $drain.truncated
      stabilize_reason = $drain.stabilize_reason
      purged_work_queue = $drain.purged_work_queue
      dead_letters_after = $drain.last_rabbit.dead_letters
      orders_after = $orders
      orders_per_sec_drain = $ordersPerSecLegacy
      consumer_tx_per_sec = $txPerSec
      consumer_tx_per_sec_busy = $busy.consumer_tx_per_sec
      consumer_tx_total = $busy.consumer_tx_total
      busy_seconds = $busy.busy_seconds
      outbox_pending_after = $drain.last_db.pending_outbox
      lock_waits_delta = $diagnostic.peaks.innodb_row_lock_waits_delta
      lock_current_wait_peak = $diagnostic.peaks.innodb_row_lock_current_waits
      campaign_id = $campaignId
    }
    $row | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $runDir "run-summary.json")
    & node (Join-Path $repo "tests\load\analyze_consumer_tx_stages.mjs") (Join-Path $runDir "lifecycle-samples.json") |
      Out-File -Encoding utf8 (Join-Path $runDir "stage-percentiles.json")
    $rows += $row
    Write-Host ("consumer={0} tx/s={1} reason={2} observe={3}s purged_wq={4} lockΔ={5}" -f $workers, $txPerSec, $drain.stabilize_reason, $drain.elapsed_seconds, $drain.purged_work_queue, $row.lock_waits_delta) -ForegroundColor Green
  }

  $rows | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $outputPath "sweep-summary.json")
  & node (Join-Path $repo "tests\load\render_consumer_sweep.mjs") $outputPath
  Write-Host ("Sweep summary: " + (Join-Path $outputPath "sweep-summary.md")) -ForegroundColor Green
}
finally {
  if (-not $KeepStack) {
    Invoke-Compose @("down", "-v", "--remove-orphans")
  }
}
