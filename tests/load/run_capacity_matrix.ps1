param(
    [string]$Cases = "baseline,buckets",
    [string]$Steps = "500,1000",
    [int]$Runs = 2,
    [string]$Project = "gofun-matrix",
    [switch]$KeepStack
)

$ErrorActionPreference = "Stop"
$matrixCases = $Cases.Split(",", [System.StringSplitOptions]::RemoveEmptyEntries) |
    ForEach-Object { $_.Trim().ToLowerInvariant() }
$allowedCases = @("baseline", "buckets")
foreach ($case in $matrixCases) {
    if ($allowedCases -notcontains $case) {
        throw "Unsupported case '$case'. Allowed: $($allowedCases -join ', ')"
    }
}
if ($matrixCases.Count -eq 0) { throw "Cases must not be empty" }
if ($Runs -le 0) { throw "Runs must be positive" }

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\")).Path
$baseCompose = (Join-Path $repo "tests\integration\docker-compose.ticketing.yml")
$capacityCompose = (Join-Path $repo "tests\load\docker-compose.capacity.yml")
$resultsRoot = (Join-Path $repo "tests\load\results\capacity-matrix-$(Get-Date -Format yyyyMMdd-HHmmss)")
$composeArgs = @("compose", "-p", $Project, "-f", $baseCompose, "-f", $capacityCompose)

function Invoke-Compose {
    param([string[]]$Arguments)
    & docker @composeArgs @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose failed with exit code $LASTEXITCODE"
    }
}

function Set-CaseEnvironment {
    param([string]$Case)
    $bucketed = $Case -eq "buckets"
    $env:INVENTORY_BUCKETS_ENABLED = if ($bucketed) { "true" } else { "false" }
    $env:INVENTORY_BUCKET_COUNT = "32"
    $env:INVENTORY_MIN_QUOTA_TO_BUCKET = "64"
    $env:INVENTORY_BUCKET_RETRY = "4"
    $env:ORDER_CONSUMER_WORKER_COUNT = "6"
    $env:ORDER_CONSUMER_PREFETCH_COUNT = "5"
    $env:ORDER_CONSUMER_MAX_RETRIES = "3"
    $env:ORDER_OUTBOX_PUBLISH_WORKERS = "4"
    $env:ORDER_OUTBOX_PUBLISH_BATCH = "200"
    $env:MYSQL_MAX_OPEN_CONNS = "100"
    $env:MYSQL_MAX_IDLE_CONNS = "10"
    $env:REDIS_POOL_SIZE = "100"
    $env:RATELIMIT_DISTRIBUTED_WRITE_ENABLED = "true"
    $env:RABBITMQ_QUEUE_TYPE = "classic"
}

function Get-Median {
    param([double[]]$Values)
    $sorted = @($Values | Sort-Object)
    if ($sorted.Count -eq 0) { return 0 }
    $middle = [int][Math]::Floor($sorted.Count / 2)
    if (($sorted.Count % 2) -eq 1) { return [double]$sorted[$middle] }
    return ([double]$sorted[$middle - 1] + [double]$sorted[$middle]) / 2
}

function Read-CaseSummary {
    param([string]$Case, [string]$RunLabel)
    $summaryPath = (Join-Path $repo "tests\load\results\$RunLabel\staircase-summary.json")
    if (-not (Test-Path $summaryPath)) { throw "Missing summary: $summaryPath" }
    $summary = Get-Content -Raw -Encoding utf8 $summaryPath | ConvertFrom-Json
    $results = @($summary.results)
    $valid = @($results | Where-Object { $_.success_rate -eq 1 -and $_.still_queued -eq 0 })
    [pscustomobject]@{
        case = $Case
        run_label = $RunLabel
        cases = $results.Count
        valid_runs = $valid.Count
        success_qps_median = [Math]::Round((Get-Median @($valid | ForEach-Object { [double]$_.success_qps })), 2)
        execute_p99_median_ms = [Math]::Round((Get-Median @($valid | ForEach-Object { [double]$_.execute.latency_ms.p99 })), 2)
        drain_seconds_median = [Math]::Round((Get-Median @($valid | ForEach-Object { [double]$_.drain_seconds })), 2)
        row_lock_waits_median = [Math]::Round((Get-Median @($valid | ForEach-Object { [double]$_.resources.mysql.row_lock_waits_delta })), 2)
        all_runs_valid = ($valid.Count -eq $results.Count -and $results.Count -gt 0)
    }
}

New-Item -ItemType Directory -Force -Path $resultsRoot | Out-Null
$summaries = @()
try {
    foreach ($case in $matrixCases) {
        Set-CaseEnvironment $case
        $runLabel = "capacity-matrix-$case-$(Get-Date -Format yyyyMMdd-HHmmss)"
        $env:BASE_URL = "http://127.0.0.1:18180/api/v1"
        $env:METRICS_URL = "http://127.0.0.1:18180/metrics"
        $env:MYSQL_CONTAINER = "${Project}-mysql-1"
        $env:REDIS_CONTAINER = "${Project}-redis-1"
        $env:RABBITMQ_CONTAINER = "${Project}-rabbitmq-1"
        $env:MONITOR_CONTAINERS = "${Project}-backend-load,${Project}-mysql-1,${Project}-redis-1,${Project}-rabbitmq-1"
        $env:CAPACITY_CONTAINER_PREFIX = $Project
        $env:GOFUN_BACKEND_PORT = "18180"
        $env:GOFUN_ELASTICSEARCH_PORT = "19301"
        $env:GOFUN_MYSQL_PORT = "13106"
        $env:GOFUN_REDIS_PORT = "16179"
        $env:GOFUN_RABBITMQ_PORT = "25772"
        $env:GOFUN_RABBITMQ_MANAGEMENT_PORT = "35772"
        $env:STEPS = $Steps
        $env:RUNS = [string]$Runs
        $env:WAVE_SIZE = "50"
        $env:POLL_SECONDS = "120"
        $env:COOLDOWN_SECONDS = "2"
        $env:SETUP_CONCURRENCY = "10"
        $casePrefix = $case.Replace("baseline-", "b").Replace("buckets-", "k").Replace("-", "")
        $env:LOAD_USER_PREFIX = "mx_${casePrefix}_"
        $env:RUN_LABEL = $runLabel
        $env:RESULTS_DIR = (Join-Path $repo "tests\load\results\$runLabel")
        $env:MATRIX_CASE = $case

        Write-Host "=== $case (transactional outbox) ===" -ForegroundColor Cyan
        Invoke-Compose @("down", "-v", "--remove-orphans")
        Invoke-Compose @("up", "-d", "--build", "--wait")
        Start-Sleep -Seconds 5
        & node (Join-Path $repo "tests\load\ticket_rush_staircase.mjs")
        if ($LASTEXITCODE -ne 0) { throw "load test failed for case $case" }
        $summaries += (Read-CaseSummary $case $runLabel)
    }

    $summaryPath = (Join-Path $resultsRoot "matrix-summary.md")
    $lines = @"
# Capacity configuration matrix (generated)

- generated_at: $(Get-Date -Format o)
- topology: one backend + one Redis + one MySQL + one RabbitMQ; ports 18180/13106/16179/25772
- STEPS: $Steps; runs per case: $Runs; request: rush execute, then wait for MQ drain
- valid run: success_rate=1 and still_queued=0; table values are medians of valid runs

| case | valid runs | success QPS median | execute p99 median | MQ drain median | MySQL row lock waits median |
|---|---:|---:|---:|---:|---:|
"@
    $lines = $lines -split "`r?`n"
    foreach ($item in $summaries) {
        $lines += "| $($item.case) | $($item.valid_runs)/$($item.cases) | $($item.success_qps_median) | $($item.execute_p99_median_ms)ms | $($item.drain_seconds_median)s | $($item.row_lock_waits_median) |"
    }
    $lines += ""
    $lines += 'Raw results: `tests/load/results/<run_label>/`.'
    $lines | Set-Content -Encoding utf8 $summaryPath
    $summaries | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 (Join-Path $resultsRoot "matrix-summary.json")
    Write-Host "Matrix summary: $summaryPath" -ForegroundColor Green
}
finally {
    if (-not $KeepStack) {
        Invoke-Compose @("down", "-v", "--remove-orphans")
    }
}
