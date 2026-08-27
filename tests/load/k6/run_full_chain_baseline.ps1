param(
  [string]$Project = "gofun-baseline",
  [string]$Vus = "100,200,500",
  [string]$Duration = "20s",
  [int]$DrainSeconds = 240,
  [int]$K6Users = 3000,
  [int]$SetupConcurrency = 100,
  [int]$PerUserLimit = 20,
  [int]$TotalQuota = 200000,
  [int]$OrderConsumerWorkers = 6,
  [int]$PaymentTimeoutWorkers = 2,
  [int]$OutboxPublishWorkers = 4,
  [int]$InventoryBucketCount = 32,
  [string]$BackendPort = "18580",
  [string]$ElasticsearchPort = "19602",
  [string]$MysqlPort = "13327",
  [string]$RedisPort = "16400",
  [string]$RabbitPort = "26073",
  [string]$RabbitManagementPort = "36073",
  [string]$OutputDir = "tests/load/results/full-chain-baseline-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [int]$InnoDBFlushLogAtTrxCommit = 1,
  [int]$SyncBinlog = 1,
  [switch]$SkipLogBin,
  [switch]$KeepStack,
  [switch]$AllowHighPerUserLimit,
  [switch]$FastPrepare,
  [switch]$K6InDocker,
  [string]$K6Image = "grafana/k6:latest"
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$baseCompose = Join-Path $repo "tests\integration\docker-compose.ticketing.yml"
$capacityCompose = Join-Path $repo "tests\load\docker-compose.capacity.yml"
$outputPath = Join-Path $repo $OutputDir
$rabbitContainer = "${Project}-rabbitmq-1"
$mysqlContainer = "${Project}-mysql-1"
$backendContainer = "${Project}-backend-load"
$baseUrl = "http://127.0.0.1:$BackendPort/api/v1"
$metricsUrl = "http://127.0.0.1:$BackendPort/metrics"
$k6Network = "${Project}_default"
$k6BaseUrl = "http://backend:8080/api/v1"
$vusList = @($Vus.Split(",") | ForEach-Object { [int]$_.Trim() } | Where-Object { $_ -gt 0 })

if ($vusList.Count -eq 0) { throw "Vus must contain positive integers" }
New-Item -ItemType Directory -Force -Path $outputPath | Out-Null

$nobinlogCompose = Join-Path $repo "tests\load\docker-compose.nobinlog.yml"
$composeArgs = @("compose", "-p", $Project, "-f", $baseCompose, "-f", $capacityCompose)
if ($SkipLogBin) {
  $composeArgs += @("-f", $nobinlogCompose)
}

function Invoke-Compose {
  param([string[]]$Arguments)
  & docker @composeArgs @Arguments
  if ($LASTEXITCODE -ne 0) { throw "docker compose failed with exit code $LASTEXITCODE" }
}

function Wait-ForBackend {
  $healthUrl = "http://127.0.0.1:$BackendPort/healthz"
  $deadline = [DateTime]::UtcNow.AddSeconds(90)
  do {
    try {
      $response = Invoke-WebRequest -UseBasicParsing -Uri $healthUrl -TimeoutSec 3
      if ($response.StatusCode -eq 200) {
        Write-Host "Backend ready: $healthUrl" -ForegroundColor DarkGreen
        return
      }
    } catch {
      # 数据库迁移初始化期间，HTTP 端口可能尚未可用。
    }
    }
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  throw "backend health check timeout: $healthUrl"
}

function Invoke-DbQuery {
  param([string]$Query)
  @(docker exec -e MYSQL_PWD=fuchang-it-mysql $mysqlContainer mysql -ufuchang -N -B fuchang_ticketing_it -e $Query 2>$null)
}

function Enable-MySqlStatementHistory {
  $query = @"
UPDATE performance_schema.setup_consumers
SET ENABLED = 'YES'
WHERE NAME IN ('events_statements_current', 'events_statements_history', 'events_statements_history_long');
"@
  docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e $query 2>$null | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "failed to enable MySQL statement history" }
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
  $delay = if ($queueTotals.Contains("fuchang.order.delay")) { $queueTotals["fuchang.order.delay"] } else { [pscustomobject]@{ ready = 0; unacknowledged = 0; total = 0 } }
  $timeout = if ($queueTotals.Contains("fuchang.order.timeout")) { $queueTotals["fuchang.order.timeout"] } else { [pscustomobject]@{ ready = 0; unacknowledged = 0; total = 0 } }
  $retry = if ($queueTotals.Contains("fuchang.it.order.retry")) { $queueTotals["fuchang.it.order.retry"] } else { [pscustomobject]@{ ready = 0; unacknowledged = 0; total = 0 } }
  $dead = if ($queueTotals.Contains("fuchang.it.order.dead")) { $queueTotals["fuchang.it.order.dead"] } else { [pscustomobject]@{ ready = 0; unacknowledged = 0; total = 0 } }
  $primaryTotal = $order.total + $delay.total + $timeout.total + $retry.total
  $workQueueTotal = $order.total + $retry.total
  [pscustomobject]@{
    messages_ready = ($queueTotals.Values | ForEach-Object { $_.ready } | Measure-Object -Sum).Sum
    messages_unacknowledged = ($queueTotals.Values | ForEach-Object { $_.unacknowledged } | Measure-Object -Sum).Sum
    total_messages = ($queueTotals.Values | ForEach-Object { $_.total } | Measure-Object -Sum).Sum
    primary_total_messages = $primaryTotal
    work_queue_total = $workQueueTotal
    dead_letters = $dead.total
    order_queue = $order.total
    delay_queue = $delay.total
    timeout_queue = $timeout.total
    retry_queue = $retry.total
  }
}

function Get-MySqlLockWaitSnapshot {
  $query = @"
SELECT
  COALESCE(CAST(r.THREAD_ID AS CHAR), ''),
  COALESCE(CAST(r.EVENT_ID AS CHAR), ''),
  COALESCE(r.OBJECT_SCHEMA, ''),
  COALESCE(r.OBJECT_NAME, ''),
  COALESCE(r.INDEX_NAME, ''),
  COALESCE(r.LOCK_TYPE, ''),
  COALESCE(r.LOCK_MODE, ''),
  COALESCE(r.LOCK_DATA, ''),
  COALESCE(CAST(b.THREAD_ID AS CHAR), ''),
  COALESCE(CAST(b.EVENT_ID AS CHAR), ''),
  COALESCE(b.OBJECT_SCHEMA, ''),
  COALESCE(b.OBJECT_NAME, ''),
  COALESCE(b.INDEX_NAME, ''),
  COALESCE(b.LOCK_TYPE, ''),
  COALESCE(b.LOCK_MODE, ''),
  COALESCE(b.LOCK_DATA, ''),
  COALESCE(REPLACE(REPLACE(REPLACE(COALESCE(NULLIF(rs.SQL_TEXT, ''), (SELECT ps.SQL_TEXT FROM performance_schema.prepared_statements_instances ps WHERE ps.OWNER_THREAD_ID = r.THREAD_ID AND ps.SQL_TEXT LIKE CONCAT('%', r.OBJECT_NAME, '%') ORDER BY ps.COUNT_EXECUTE DESC LIMIT 1), NULLIF(rh.SQL_TEXT, ''), rhl.SQL_TEXT, (SELECT s.SQL_TEXT FROM performance_schema.events_statements_history s WHERE s.THREAD_ID = r.THREAD_ID AND s.EVENT_ID <= r.EVENT_ID ORDER BY s.EVENT_ID DESC LIMIT 1), (SELECT s.SQL_TEXT FROM performance_schema.events_statements_history_long s WHERE s.THREAD_ID = r.THREAD_ID AND s.EVENT_ID <= r.EVENT_ID ORDER BY s.EVENT_ID DESC LIMIT 1)), CHAR(9), ' '), CHAR(10), ' '), CHAR(13), ' '), ''),
  COALESCE(REPLACE(REPLACE(REPLACE(COALESCE(NULLIF(bs.SQL_TEXT, ''), (SELECT ps.SQL_TEXT FROM performance_schema.prepared_statements_instances ps WHERE ps.OWNER_THREAD_ID = b.THREAD_ID AND ps.SQL_TEXT LIKE CONCAT('%', b.OBJECT_NAME, '%') ORDER BY ps.COUNT_EXECUTE DESC LIMIT 1), NULLIF(bh.SQL_TEXT, ''), bhl.SQL_TEXT, (SELECT s.SQL_TEXT FROM performance_schema.events_statements_history s WHERE s.THREAD_ID = b.THREAD_ID AND s.EVENT_ID <= b.EVENT_ID ORDER BY s.EVENT_ID DESC LIMIT 1), (SELECT s.SQL_TEXT FROM performance_schema.events_statements_history_long s WHERE s.THREAD_ID = b.THREAD_ID AND s.EVENT_ID <= b.EVENT_ID ORDER BY s.EVENT_ID DESC LIMIT 1)), CHAR(9), ' '), CHAR(10), ' '), CHAR(13), ' '), '')
FROM performance_schema.data_lock_waits w
JOIN performance_schema.data_locks r
  ON r.ENGINE = w.ENGINE
 AND r.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
JOIN performance_schema.data_locks b
  ON b.ENGINE = w.ENGINE
 AND b.ENGINE_LOCK_ID = w.BLOCKING_ENGINE_LOCK_ID
LEFT JOIN performance_schema.events_statements_current rs
  ON rs.THREAD_ID = r.THREAD_ID AND rs.EVENT_ID = r.EVENT_ID
LEFT JOIN performance_schema.events_statements_current bs
  ON bs.THREAD_ID = b.THREAD_ID AND bs.EVENT_ID = b.EVENT_ID
LEFT JOIN performance_schema.events_statements_history rh
  ON rh.THREAD_ID = r.THREAD_ID AND rh.EVENT_ID = r.EVENT_ID
LEFT JOIN performance_schema.events_statements_history bh
  ON bh.THREAD_ID = b.THREAD_ID AND bh.EVENT_ID = b.EVENT_ID
LEFT JOIN performance_schema.events_statements_history_long rhl
  ON rhl.THREAD_ID = r.THREAD_ID AND rhl.EVENT_ID = r.EVENT_ID
LEFT JOIN performance_schema.events_statements_history_long bhl
  ON bhl.THREAD_ID = b.THREAD_ID AND bhl.EVENT_ID = b.EVENT_ID
WHERE w.ENGINE = 'INNODB';
"@
  $rows = @(docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B fuchang_ticketing_it -e $query 2>$null)
  $result = @()
  foreach ($row in $rows) {
    $parts = $row -split "`t", 18
    if ($parts.Count -lt 18) { continue }
    $result += [pscustomobject]@{
      requesting_thread_id = $parts[0]
      requesting_event_id = $parts[1]
      requesting_schema = $parts[2]
      requesting_table = $parts[3]
      requesting_index = $parts[4]
      requesting_lock_type = $parts[5]
      requesting_lock_mode = $parts[6]
      requesting_lock_data = $parts[7]
      blocking_thread_id = $parts[8]
      blocking_event_id = $parts[9]
      blocking_schema = $parts[10]
      blocking_table = $parts[11]
      blocking_index = $parts[12]
      blocking_lock_type = $parts[13]
      blocking_lock_mode = $parts[14]
      blocking_lock_data = $parts[15]
      requesting_sql = $parts[16]
      blocking_sql = $parts[17]
    }
  }
  return $result
}

function Get-PrometheusSnapshot {
  try {
    $text = (Invoke-WebRequest -UseBasicParsing -Uri $metricsUrl -TimeoutSec 3).Content
    $result = [ordered]@{}
    $matches = [regex]::Matches($text, '(?m)^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{([^}]*)\})?\s+([0-9.eE+-]+)$')
    foreach ($match in $matches) {
      $name = $match.Groups[1].Value
      $labels = $match.Groups[2].Value
      $key = if ([string]::IsNullOrWhiteSpace($labels)) { $name } else { "$name{$labels}" }
      $result[$key] = [double]$match.Groups[3].Value
    }
    return [pscustomobject]$result
  } catch {
    return [pscustomobject]@{}
  }
}

function Get-DbSnapshot {
  param([string]$CampaignId)
  $orderCount = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order WHERE rush_sale_campaign_id = $CampaignId;")
  $outboxCount = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order_outbox o JOIN ticket_order t ON t.id = o.order_id WHERE t.rush_sale_campaign_id = $CampaignId;")
  $orderStatusRows = @(Invoke-DbQuery "SELECT status, COUNT(*) FROM ticket_order WHERE rush_sale_campaign_id = $CampaignId GROUP BY status ORDER BY status;")
  $outboxStatusRows = @(Invoke-DbQuery "SELECT o.status, COUNT(*) FROM ticket_order_outbox o JOIN ticket_order t ON t.id = o.order_id WHERE t.rush_sale_campaign_id = $CampaignId GROUP BY o.status ORDER BY o.status;")
  $pendingOutbox = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order_outbox o JOIN ticket_order t ON t.id = o.order_id WHERE t.rush_sale_campaign_id = $CampaignId AND o.status IN ('pending','publishing');")
  $inventoryBuckets = @(Invoke-DbQuery "SELECT COUNT(*) FROM rush_campaign_bucket WHERE campaign_id = $CampaignId;")

  function Convert-StatusRows([object[]]$rows) {
    $result = [ordered]@{}
    foreach ($row in $rows) {
      $parts = ($row -split "\s+") | Where-Object { $_ -ne "" }
      if ($parts.Count -ge 2) { $result[$parts[0]] = [int64]$parts[1] }
    }
    return [pscustomobject]$result
  }

  [pscustomobject]@{
    campaign_id = $CampaignId
    orders = if ($orderCount.Count -gt 0) { [int64]$orderCount[0] } else { 0 }
    outbox = if ($outboxCount.Count -gt 0) { [int64]$outboxCount[0] } else { 0 }
    pending_outbox = if ($pendingOutbox.Count -gt 0) { [int64]$pendingOutbox[0] } else { 0 }
    inventory_buckets = if ($inventoryBuckets.Count -gt 0) { [int64]$inventoryBuckets[0] } else { 0 }
    order_status = Convert-StatusRows $orderStatusRows
    outbox_status = Convert-StatusRows $outboxStatusRows
  }
}

function Get-Maximum([object[]]$Values) {
  $numbers = @($Values | Where-Object { $_ -ne $null } | ForEach-Object { [double]$_ })
  if ($numbers.Count -eq 0) { return 0 }
  return ($numbers | Measure-Object -Maximum).Maximum
}

function Invoke-Diagnostic {
  param([int]$CurrentVus, [string]$RunDir)
  $diagnosticPath = Join-Path $repo "tests\load\k6\run_peak_diagnostic.ps1"
  $diagnosticArgs = @(
    "-Vus", $CurrentVus,
    "-Duration", $Duration,
    "-BaseUrl", $(if ($K6InDocker) { $k6BaseUrl } else { $baseUrl }),
    "-MetricsUrl", $metricsUrl,
    "-RabbitContainer", $rabbitContainer,
    "-MysqlContainer", $mysqlContainer,
    "-OutputDir", $RunDir
  )
  if ($K6InDocker) {
    $diagnosticArgs += @(
      "-K6InDocker",
      "-K6Network", $k6Network,
      "-K6Image", $K6Image
    )
  }
  & powershell -NoProfile -ExecutionPolicy Bypass -File $diagnosticPath @diagnosticArgs
  if ($LASTEXITCODE -ne 0) { throw "diagnostic failed for VUS=$CurrentVus" }
}

function Wait-ForDrain {
  param([string]$CampaignId, [string]$RunPath)
  $drainSamples = [System.Collections.Generic.List[object]]::new()
  $zeroStreak = 0
  $started = Get-Date
  while (((Get-Date) - $started).TotalSeconds -lt $DrainSeconds) {
    $rabbit = Get-RabbitSnapshot
    $db = Get-DbSnapshot $CampaignId
    $sample = [pscustomobject]@{
      timestamp = (Get-Date).ToString("o")
      phase = "drain"
      elapsed_seconds = [Math]::Round(((Get-Date) - $started).TotalSeconds, 1)
      rabbitmq = $rabbit
      mysql_lock_waits = @(Get-MySqlLockWaitSnapshot)
      prometheus = Get-PrometheusSnapshot
      db = $db
    }
    $drainSamples.Add($sample)
    if ($rabbit.work_queue_total -eq 0 -and $db.pending_outbox -eq 0) {
      $zeroStreak++
      if ($zeroStreak -ge 3) { break }
    } else {
      $zeroStreak = 0
    }
    Start-Sleep -Seconds 2
  }
  $drainSamples | ConvertTo-Json -Depth 12 | Set-Content -Encoding utf8 (Join-Path $RunPath "drain-samples.json")
  $lockWaitRows = @($drainSamples | ForEach-Object { @($_.mysql_lock_waits) })
  $lockWaitRows | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $RunPath "mysql-lock-waits.json")
  [pscustomobject]@{
    elapsed_seconds = if ($drainSamples.Count -gt 0) { $drainSamples[-1].elapsed_seconds } else { 0 }
    primary_drained = ($drainSamples.Count -gt 0 -and $drainSamples[-1].rabbitmq.work_queue_total -eq 0 -and $drainSamples[-1].db.pending_outbox -eq 0)
    completed = ($drainSamples.Count -gt 0 -and $drainSamples[-1].rabbitmq.work_queue_total -eq 0 -and $drainSamples[-1].db.pending_outbox -eq 0 -and $drainSamples[-1].rabbitmq.dead_letters -eq 0)
    max_rabbit_total = Get-Maximum @($drainSamples | ForEach-Object { $_.rabbitmq.total_messages })
    max_primary_total = Get-Maximum @($drainSamples | ForEach-Object { $_.rabbitmq.primary_total_messages })
    max_work_queue_total = Get-Maximum @($drainSamples | ForEach-Object { $_.rabbitmq.work_queue_total })
    max_dead_letters = Get-Maximum @($drainSamples | ForEach-Object { $_.rabbitmq.dead_letters })
    max_rabbit_ready = Get-Maximum @($drainSamples | ForEach-Object { $_.rabbitmq.messages_ready })
    last_rabbit = if ($drainSamples.Count -gt 0) { $drainSamples[-1].rabbitmq } else { Get-RabbitSnapshot }
    last_db = if ($drainSamples.Count -gt 0) { $drainSamples[-1].db } else { Get-DbSnapshot $CampaignId }
  }
}

function Save-LifecycleMetrics {
  param([string]$RunPath)
  $httpSamples = Get-Content (Join-Path $RunPath "samples.json") -Raw -Encoding utf8 | ConvertFrom-Json
  $drainSamples = Get-Content (Join-Path $RunPath "drain-samples.json") -Raw -Encoding utf8 | ConvertFrom-Json
  $lifecycle = [System.Collections.Generic.List[object]]::new()
  foreach ($sample in $httpSamples) { $lifecycle.Add($sample) }
  foreach ($sample in $drainSamples) { $lifecycle.Add($sample) }
  $lifecycle | ConvertTo-Json -Depth 20 | Set-Content -Encoding utf8 (Join-Path $RunPath "lifecycle-samples.json")
  $first = if ($lifecycle.Count -gt 0) { $lifecycle[0] } else { $null }
  $last = if ($lifecycle.Count -gt 0) { $lifecycle[$lifecycle.Count - 1] } else { $null }
  [pscustomobject]@{
    sample_count = $lifecycle.Count
    http_sample_count = @($httpSamples).Count
    drain_sample_count = @($drainSamples).Count
    first_timestamp = if ($null -ne $first) { $first.timestamp } else { $null }
    last_timestamp = if ($null -ne $last) { $last.timestamp } else { $null }
    prometheus_captured_through_drain = ($drainSamples.Count -gt 0 -and $null -ne $drainSamples[0].prometheus)
  }
}

function Save-FailureDiagnostics {
  param([string]$CampaignId, [string]$RunPath)
  $backendLogPath = Join-Path $RunPath "backend.log"
  $backendLogCommand = "docker logs $backendContainer > `"$backendLogPath`" 2>&1"
  cmd.exe /d /c $backendLogCommand | Out-Null
  $outboxErrors = Invoke-DbQuery "SELECT o.id, o.order_id, o.status, o.attempts, o.last_error FROM ticket_order_outbox o JOIN ticket_order t ON t.id=o.order_id WHERE t.rush_sale_campaign_id=$CampaignId AND (o.status <> 'published' OR o.last_error <> '') ORDER BY o.id;"
  $outboxErrors | Set-Content -Encoding utf8 (Join-Path $RunPath "outbox-errors.tsv")
  $deadPath = Join-Path $RunPath "dead-letters.txt"
  $deadCommand = "docker exec $rabbitContainer rabbitmqadmin -H 127.0.0.1 -P 15672 -u fuchang -p fuchang-it-rabbit -V / get queue=fuchang.it.order.dead count=100 ackmode=ack_requeue_true > `"$deadPath`" 2>&1"
  cmd.exe /d /c $deadCommand | Out-Null
}

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
$env:ORDER_CONSUMER_WORKER_COUNT = [string]$OrderConsumerWorkers
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

$rows = @()
try {
  Invoke-Compose @("down", "-v", "--remove-orphans")
  Invoke-Compose @("up", "-d", "--build", "--wait")
  Wait-ForBackend
  Enable-MySqlStatementHistory
  if (-not $SkipLogBin) {
    docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit; SET GLOBAL sync_binlog=$SyncBinlog;" 2>$null | Out-Null
  } else {
    docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit;" 2>$null | Out-Null
  }
  $durability = @(docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SHOW VARIABLES WHERE Variable_name IN ('innodb_flush_log_at_trx_commit','sync_binlog','log_bin','log_bin_basename');" 2>$null)
  Write-Host ("MySQL durability: " + ($durability -join " | ")) -ForegroundColor DarkYellow
  if ($SkipLogBin) {
    $logBinOff = ($durability | Where-Object { $_ -match '^log_bin\s+OFF$' -or $_ -match '^log_bin\tOFF$' })
    if (-not $logBinOff) {
      throw "SkipLogBin requested but log_bin is not OFF"
    }
    Write-Host "WARNING: binary logging disabled (--skip-log-bin); diagnostic only." -ForegroundColor Yellow
  } elseif ($InnoDBFlushLogAtTrxCommit -ne 1 -or $SyncBinlog -ne 1) {
    Write-Host "WARNING: non-default durability is for diagnostic comparison only." -ForegroundColor Yellow
  }

  foreach ($currentVus in $vusList) {
    $runName = "vus-$currentVus"
    $runLabel = "full-chain-baseline-$currentVus-$(Get-Date -Format yyyyMMdd-HHmmss)"
    $runDirRelative = Join-Path $OutputDir $runName
    $runDir = Join-Path $repo $runDirRelative
    New-Item -ItemType Directory -Force -Path $runDir | Out-Null
    $env:BASE_URL = $baseUrl
    $env:K6_USERS = [string]$K6Users
    $env:RUSH_TOTAL_QUOTA = [string]$TotalQuota
    $env:RUSH_PER_USER_LIMIT = [string]$PerUserLimit
    $env:SETUP_CONCURRENCY = [string]$SetupConcurrency
    $env:RUSH_LABEL = $runLabel
    if ($AllowHighPerUserLimit) {
      $env:K6_ALLOW_HIGH_PER_USER_LIMIT = "1"
    } else {
      Remove-Item Env:K6_ALLOW_HIGH_PER_USER_LIMIT -ErrorAction SilentlyContinue
    }

    Write-Host "=== prepare VUS=$currentVus ===" -ForegroundColor Cyan
    $prepareScript = if ($FastPrepare) {
      Join-Path $repo "tests\load\k6\prepare_rush_fixture_fast.mjs"
    } else {
      Join-Path $repo "tests\load\k6\prepare_rush_fixture.mjs"
    }
    & node $prepareScript
    if ($LASTEXITCODE -ne 0) { throw "fixture preparation failed for VUS=$currentVus" }
    $fixture = Get-Content (Join-Path $repo "tests\load\k6\fixtures\rush_execute.json") -Raw -Encoding utf8 | ConvertFrom-Json
    $campaignId = [string]$fixture.campaign_id
    $beforeDb = Get-DbSnapshot $campaignId

    Write-Host "=== diagnostic VUS=$currentVus ===" -ForegroundColor Cyan
    Invoke-Diagnostic $currentVus $runDirRelative
    $diagnostic = Get-Content (Join-Path $runDir "diagnostic-summary.json") -Raw -Encoding utf8 | ConvertFrom-Json
    $k6 = $diagnostic.summary

    Write-Host "=== drain VUS=$currentVus ===" -ForegroundColor Cyan
    $drain = Wait-ForDrain $campaignId $runDir
    $lifecycle = Save-LifecycleMetrics $runDir
    $afterDb = Get-DbSnapshot $campaignId
    Save-FailureDiagnostics $campaignId $runDir
    $row = [pscustomobject]@{
      vus = $currentVus
      consumer_workers = $OrderConsumerWorkers
      bucket_count = $InventoryBucketCount
      campaign_id = $campaignId
      http_reqs = $k6.http_reqs
      http_req_rate = $k6.http_req_rate
      http_200_estimate = [Math]::Round(([double]$k6.http_reqs * [double]$k6.rush_execute_success_rate), 0)
      http_success_rate = $k6.rush_execute_success_rate
      transport_errors = $k6.rush_execute_transport_error
      business_rejected = $k6.rush_execute_business_rejected
      server_errors = $k6.rush_execute_server_error
      p99_ms = $k6.p99_ms
      http_window_mq_peak = $diagnostic.peaks.rabbitmq_total_messages
      http_window_work_queue_peak = $diagnostic.peaks.rabbitmq_work_queue_total
      post_test_mq_peak = $drain.max_rabbit_total
      post_test_primary_mq_peak = $drain.max_primary_total
      post_test_work_queue_peak = $drain.max_work_queue_total
      drain_seconds = $drain.elapsed_seconds
      primary_drained = $drain.primary_drained
      drain_completed = $drain.completed
      dead_letters_after = $drain.last_rabbit.dead_letters
      mq_work_queue_after = $drain.last_rabbit.work_queue_total
      mq_primary_total_after = $drain.last_rabbit.primary_total_messages
      mq_delay_queue_after = $drain.last_rabbit.delay_queue
      mq_timeout_queue_after = $drain.last_rabbit.timeout_queue
      outbox_before = $beforeDb.outbox
      outbox_after = $afterDb.outbox
      outbox_pending_after = $afterDb.pending_outbox
      inventory_buckets_after = $afterDb.inventory_buckets
      orders_after = $afterDb.orders
      order_status_after = $afterDb.order_status
      outbox_status_after = $afterDb.outbox_status
      lock_current_wait_peak = $diagnostic.peaks.innodb_row_lock_current_waits
      lock_waits_delta = $diagnostic.peaks.innodb_row_lock_waits_delta
      prometheus_samples = $lifecycle.sample_count
      prometheus_http_samples = $lifecycle.http_sample_count
      prometheus_drain_samples = $lifecycle.drain_sample_count
      prometheus_captured_through_drain = $lifecycle.prometheus_captured_through_drain
    }
    $row | ConvertTo-Json -Depth 12 | Set-Content -Encoding utf8 (Join-Path $runDir "run-summary.json")
    $rows += $row
  }

  $rows | ConvertTo-Json -Depth 12 | Set-Content -Encoding utf8 (Join-Path $outputPath "baseline-summary.json")
  $md = @(
    "# Full-chain baseline",
    "",
    "- generated_at: $(Get-Date -Format o)",
    "- topology: one backend + one Redis + one MySQL + one RabbitMQ + Elasticsearch",
    "- configuration: same-db inventory buckets enabled, transactional outbox, $OrderConsumerWorkers order workers, prefetch 5, $PaymentTimeoutWorkers payment-timeout workers, $OutboxPublishWorkers outbox publisher workers",
    "- durability: innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit sync_binlog=$SyncBinlog skip_log_bin=$SkipLogBin",
    "- duration per VUS: $Duration; drain timeout: ${DrainSeconds}s",
    "",
    "| VUS | Consumer | buckets | HTTP req/s | HTTP 200 est. | transport errors | p99 | HTTP-window MQ peak | post-test MQ peak | drain(work queue) | orders | pending outbox | lock waits |",
    "|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|"
  )
  foreach ($item in $rows) {
    $md += "| $($item.vus) | $($item.consumer_workers) | $($item.bucket_count) | $([Math]::Round([double]$item.http_req_rate, 1)) | $($item.http_200_estimate) | $($item.transport_errors) | $([Math]::Round([double]$item.p99_ms, 1))ms | $($item.http_window_mq_peak) | $($item.post_test_mq_peak) | $([Math]::Round([double]$item.drain_seconds, 1))s / primary=$($item.primary_drained) / dead=$($item.dead_letters_after) | $($item.orders_after) | $($item.outbox_pending_after) | $($item.lock_waits_delta) |"
  }
  $md += ""
  $md += "Raw per-VUS artifacts: $OutputDir/"
  $md | Set-Content -Encoding utf8 (Join-Path $outputPath "baseline-summary.md")
  Write-Host "Baseline summary: $(Join-Path $outputPath 'baseline-summary.md')" -ForegroundColor Green
}
finally {
  if (-not $KeepStack) {
    Invoke-Compose @("down", "-v", "--remove-orphans")
  }
}
