param(
  [int]$Rate = 400,
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
  [string]$OutputDir = "tests/load/results/mixed-spot-$Rate-rps-$(Get-Date -Format yyyyMMdd-HHmmss)",
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
      throw "backend exited: $state"
    }
    try {
      if ((Invoke-WebRequest -UseBasicParsing -Uri $healthUrl -TimeoutSec 3).StatusCode -eq 200) { return }
    } catch {}
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  throw "backend health timeout"
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
    order_ready = $orderReady
    order_unacked = $orderUnacked
    work_queue_total = $orderReady + $orderUnacked
  }
}

function Get-DbSnapshot([string]$CampaignId) {
  $orders = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order WHERE rush_sale_campaign_id=$CampaignId OR rush_sale_campaign_id IS NULL;")
  $pendingPayment = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order WHERE status='pending_payment';")
  $pendingOutbox = @(Invoke-DbQuery "SELECT COUNT(*) FROM ticket_order_outbox WHERE status IN ('pending','publishing');")
  [pscustomobject]@{
    orders = if ($orders.Count) { [int64]$orders[0] } else { 0 }
    pending_payment = if ($pendingPayment.Count) { [int64]$pendingPayment[0] } else { 0 }
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
  Invoke-Compose @("up", "-d", "--no-deps", "--force-recreate", "backend")
}
Wait-ForBackend
docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit;" 2>$null | Out-Null
Write-Host ("Mixed spot: rate=${Rate}/s duration=$Duration consumers=$ConsumerWorkers") -ForegroundColor Cyan

Clear-Residue
Invoke-Compose @("up", "-d", "--no-deps", "--force-recreate", "backend")
Wait-ForBackend
docker exec -e MYSQL_PWD=fuchang-it-root $mysqlContainer mysql -uroot -N -B -e "SET GLOBAL innodb_flush_log_at_trx_commit=$InnoDBFlushLogAtTrxCommit;" 2>$null | Out-Null

$env:BASE_URL = $baseUrl
$env:K6_USERS = "3000"
$env:RUSH_TOTAL_QUOTA = "200000"
$env:RUSH_PER_USER_LIMIT = "20"
$env:RUSH_LABEL = "mixed-spot-$Rate-$(Get-Date -Format yyyyMMdd-HHmmss)"
$prepareScript = if ($FastPrepare) {
  Join-Path $repo "tests\load\k6\prepare_rush_fixture_fast.mjs"
} else {
  Join-Path $repo "tests\load\k6\prepare_rush_fixture.mjs"
}
& node $prepareScript
if ($LASTEXITCODE -ne 0) { throw "fixture failed" }

# Enrich fixture with city for mixed script.
$fixturePath = Join-Path $repo "tests\load\k6\fixtures\rush_execute.json"
$fixture = Get-Content $fixturePath -Raw -Encoding utf8 | ConvertFrom-Json
if (-not $fixture.city) {
  $fixture | Add-Member -NotePropertyName city -NotePropertyValue "武汉" -Force
  $json = $fixture | ConvertTo-Json -Depth 8
  [System.IO.File]::WriteAllText($fixturePath, $json + "`n", [System.Text.UTF8Encoding]::new($false))
}
$campaignId = [string]$fixture.campaign_id

$samples = [System.Collections.Generic.List[object]]::new()
$started = Get-Date
$samples.Add((Get-Sample "pre_http" $started $campaignId))

$summaryPath = Join-Path $outputPath "k6-summary.json"
$k6Stdout = Join-Path $outputPath "k6.stdout.log"
$k6Stderr = Join-Path $outputPath "k6.stderr.log"
$k6Root = Join-Path $repo "tests\load\k6"
$preVUs = [Math]::Max($Rate, 80)
$maxVUs = [Math]::Max($Rate * 2, $preVUs)

if (-not $K6InDocker) { throw "use -K6InDocker" }
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
  "--env", "CITY=武汉",
  $K6Image, "run", "/work/mixed_traffic.js"
)
$process = Start-Process -FilePath "docker" -ArgumentList $k6Args -WorkingDirectory $repo -NoNewWindow -PassThru `
  -RedirectStandardOutput $k6Stdout -RedirectStandardError $k6Stderr

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
try {
  & node (Join-Path $repo "tests\load\analyze_consumer_tx_stages.mjs") (Join-Path $outputPath "lifecycle-samples.json") |
    Out-File -Encoding utf8 (Join-Path $outputPath "stage-percentiles.json")
} catch {
  Write-Host "stage analyze skipped: $($_.Exception.Message)" -ForegroundColor DarkYellow
}

if (-not (Test-Path $summaryPath)) {
  throw "k6 summary missing; see k6.stderr.log"
}
$k6 = Get-Content $summaryPath -Raw -Encoding utf8 | ConvertFrom-Json
$stages = $null
$stagePath = Join-Path $outputPath "stage-percentiles.json"
if (Test-Path $stagePath) {
  try {
    $stages = Get-Content $stagePath -Raw -Encoding utf8 | ConvertFrom-Json
  } catch {}
}

$httpSamples = @($samples | Where-Object { $_.phase -eq "http" })
$firstHttp = $httpSamples[0]
$lastHttp = $httpSamples[-1]
$lastAll = $samples[-1]
$httpDt = [Math]::Max(0.001, $lastHttp.elapsed_seconds - $firstHttp.elapsed_seconds)
$consumerDelta = $lastHttp.consumer_tx_total - $firstHttp.consumer_tx_total
$consumerRate = [Math]::Round($consumerDelta / $httpDt, 1)
$readyPeak = ($samples | ForEach-Object { $_.rabbitmq.order_ready } | Measure-Object -Maximum).Maximum
$outboxPeak = ($samples | ForEach-Object { $_.db.pending_outbox } | Measure-Object -Maximum).Maximum
$ppDelta = $lastHttp.db.pending_payment - $firstHttp.db.pending_payment

$txP95 = $null; $txP99 = $null
if ($stages -and $stages.whole_transaction) {
  $txP95 = $stages.whole_transaction.p95_ms
  $txP99 = $stages.whole_transaction.p99_ms
}

$report = [ordered]@{
  config = @{
    target_rate = $Rate
    duration = $Duration
    consumer_workers = $ConsumerWorkers
    bucket_count = $InventoryBucketCount
    model = "mixed_traffic_v1"
    weights = $k6.weights
  }
  http = @{
    achieved_req_rate = $k6.http_req_rate
    http_reqs = $k6.http_reqs
    p50_ms = $k6.p50_ms
    p95_ms = $k6.p95_ms
    p99_ms = $k6.p99_ms
    action_ok_rate = $k6.mixed_action_ok_rate
    action_counts = $k6.action_counts
  }
  consumer = @{
    complete_rate_during_http = $consumerRate
    tx_delta_during_http = [Math]::Round($consumerDelta, 0)
    tx_p95_ms = $txP95
    tx_p99_ms = $txP99
  }
  queues = @{
    order_ready_peak = $readyPeak
    order_ready_end_http = $lastHttp.rabbitmq.order_ready
    outbox_pending_peak = $outboxPeak
    outbox_pending_end_http = $lastHttp.db.pending_outbox
  }
  pending_payment = @{
    delta_during_http = $ppDelta
    end_http = $lastHttp.db.pending_payment
    end = $lastAll.db.pending_payment
  }
  k6_exit_code = $process.ExitCode
  http_wall_seconds = [Math]::Round(($httpEnd - $started).TotalSeconds, 1)
}
$report | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 (Join-Path $outputPath "mixed-spot-summary.json")

$counts = $k6.action_counts
$countLines = if ($counts) {
  ($counts.PSObject.Properties | ForEach-Object { "- $($_.Name): $($_.Value)" }) -join "`n"
} else { "- (n/a)" }

$md = @"
# Mixed traffic spot: ${Rate} req/s x $Duration

固定：Consumer=$ConsumerWorkers, bucket=$InventoryBucketCount, outbox=$OutboxPublishWorkers, nobinlog+redo=1

## 流量比例（设计 v2）
| 动作 | 占比 | 说明 |
| --- | ---: | --- |
| event_detail | 25% | 场次/票档详情 |
| events_list | 22% | 同城列表刷新/翻页 |
| rush_execute_ok | 12% | 合格秒杀 |
| orders_list | 8% | 我的订单 |
| order_detail | 8% | 订单详情 |
| rush_sales_list | 6% | 抢票列表 |
| login | 4% | 登录 |
| normal_order | 4% | 普通下单 |
| catalog_meta | 3% | 城市元数据 |
| rush_execute_bad | 3% | 不合格秒杀 |
| register | 3% | 注册 |
| events_list_switch_city | 2% | 真正换城市 |

## 结果
| 指标 | 值 |
| --- | ---: |
| HTTP 输入速率 | $([Math]::Round([double]$k6.http_req_rate, 1)) |
| HTTP 总请求 | $($k6.http_reqs) |
| HTTP P50/P95/P99 | $([Math]::Round([double]$k6.p50_ms,1)) / $([Math]::Round([double]$k6.p95_ms,1)) / $([Math]::Round([double]$k6.p99_ms,1)) ms |
| action OK rate | $($k6.mixed_action_ok_rate) |
| Consumer 完成速率 (HTTP窗) | $consumerRate tx/s |
| Consumer tx P95/P99 | $txP95 / $txP99 ms |
| RabbitMQ order ready peak | $readyPeak |
| Outbox pending peak | $outboxPeak |
| pending_payment Δ (HTTP窗) | $ppDelta |

## 各动作次数
$countLines
"@
# Write UTF8 without BOM issues for console; file for user
[System.IO.File]::WriteAllText((Join-Path $outputPath "mixed-spot-summary.md"), $md, [System.Text.UTF8Encoding]::new($false))
Write-Host ("Mixed summary: " + (Join-Path $outputPath "mixed-spot-summary.md")) -ForegroundColor Green
$report | ConvertTo-Json -Depth 6 | Write-Host

if (-not $KeepStack -and -not $ReuseStack) {
  Invoke-Compose @("down", "-v", "--remove-orphans")
}
