param(
  [string]$Rates = "800,1000",
  [int]$Repeats = 2,
  [string]$Duration = "60s",
  [int]$ConsumerWorkers = 8,
  [int]$HttpMaxOpenConns = 70,
  [int]$WorkerMaxOpenConns = 30,
  [switch]$SkipLogBin,
  [string]$Project = "gofun-csweep",
  [string]$OutputDir = "tests/load/results/mixed-order-opt-verify-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [switch]$KeepStack
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$outputPath = Join-Path $repo $OutputDir
New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
$profileScript = Join-Path $PSScriptRoot "run_mixed_resource_profile.ps1"
$histScript = Join-Path $repo "tests\load\analyze_prom_hist_delta.mjs"
$rateList = @($Rates.Split(",") | ForEach-Object { [int]$_.Trim() } | Where-Object { $_ -gt 0 })

$allRows = @()
foreach ($rate in $rateList) {
  for ($rep = 1; $rep -le $Repeats; $rep++) {
    Write-Host ("===== order-opt verify rate={0} rep={1} =====" -f $rate, $rep) -ForegroundColor Cyan
    $runDirRel = Join-Path $OutputDir ("rate-{0}-r{1}" -f $rate, $rep)
    $args = @(
      "-Rates", [string]$rate,
      "-Duration", $Duration,
      "-ConsumerWorkers", $ConsumerWorkers,
      "-HttpMaxOpenConns", $HttpMaxOpenConns,
      "-WorkerMaxOpenConns", $WorkerMaxOpenConns,
      "-Project", $Project,
      "-OutputDir", $runDirRel,
      "-KeepStack"
    )
    if ($SkipLogBin) { $args += "-SkipLogBin" }
    & powershell -NoProfile -ExecutionPolicy Bypass -File $profileScript @args
    if ($LASTEXITCODE -ne 0) { throw "profile failed rate=$rate rep=$rep" }

    $runDir = Join-Path $repo $runDirRel
    $rateDir = Join-Path $runDir ("rate-" + $rate)
    $row = Get-Content (Join-Path $rateDir "profile-row.json") -Raw -Encoding utf8 | ConvertFrom-Json
    $stagePath = Join-Path $rateDir "consumer-stages.json"
    & node $histScript (Join-Path $rateDir "prom-before.json") (Join-Path $rateDir "prom-after.json") $stagePath
    $stages = Get-Content $stagePath -Raw -Encoding utf8 | ConvertFrom-Json

    $allRows += [pscustomobject]@{
      rate = $rate
      rep = $rep
      achieved_req_rate = $row.achieved_req_rate
      http_p95_ms = $row.http_p95_ms
      http_p99_ms = $row.http_p99_ms
      consumer_tx_per_sec = $row.consumer_tx_per_sec
      order_ready_peak = $row.order_ready_peak
      order_ready_slope_per_s = $row.order_ready_slope_per_s
      http_pool_in_use_peak = $row.http_pool_in_use_peak
      http_pool_wait_count_delta = $row.http_pool_wait_count_delta
      worker_pool_in_use_peak = $row.worker_pool_in_use_peak
      worker_pool_wait_count_delta = $row.worker_pool_wait_count_delta
      tx_p50_ms = $stages.tx_success.p50_ms
      tx_p95_ms = $stages.tx_success.p95_ms
      tx_p99_ms = $stages.tx_success.p99_ms
      commit_p50_ms = $stages.commit.p50_ms
      commit_p95_ms = $stages.commit.p95_ms
      commit_p99_ms = $stages.commit.p99_ms
      order_lock_p95_ms = $stages.order_lock.p95_ms
      rush_bucket_p95_ms = $stages.rush_bucket_update.p95_ms
      rush_bucket_p99_ms = $stages.rush_bucket_update.p99_ms
      tier_bucket_p95_ms = $stages.tier_bucket_update.p95_ms
    }
  }
}

function Median([double[]]$values) {
  $sorted = @($values | Sort-Object)
  if ($sorted.Count -eq 0) { return $null }
  if ($sorted.Count % 2 -eq 1) { return $sorted[[int]($sorted.Count / 2)] }
  return [Math]::Round(($sorted[$sorted.Count / 2 - 1] + $sorted[$sorted.Count / 2]) / 2.0, 3)
}

$medians = @()
foreach ($rate in $rateList) {
  $subset = @($allRows | Where-Object { $_.rate -eq $rate })
  $medians += [pscustomobject]@{
    rate = $rate
    n = $subset.Count
    achieved_req_rate = Median @($subset | ForEach-Object { [double]$_.achieved_req_rate })
    http_p95_ms = Median @($subset | ForEach-Object { [double]$_.http_p95_ms })
    http_p99_ms = Median @($subset | ForEach-Object { [double]$_.http_p99_ms })
    consumer_tx_per_sec = Median @($subset | ForEach-Object { [double]$_.consumer_tx_per_sec })
    order_ready_peak = Median @($subset | ForEach-Object { [double]$_.order_ready_peak })
    order_ready_slope_per_s = Median @($subset | ForEach-Object { [double]$_.order_ready_slope_per_s })
    http_pool_in_use_peak = Median @($subset | ForEach-Object { [double]$_.http_pool_in_use_peak })
    http_pool_wait_count_delta = Median @($subset | ForEach-Object { [double]$_.http_pool_wait_count_delta })
    worker_pool_in_use_peak = Median @($subset | ForEach-Object { [double]$_.worker_pool_in_use_peak })
    worker_pool_wait_count_delta = Median @($subset | ForEach-Object { [double]$_.worker_pool_wait_count_delta })
    tx_p50_ms = Median @($subset | ForEach-Object { [double]$_.tx_p50_ms })
    tx_p95_ms = Median @($subset | ForEach-Object { [double]$_.tx_p95_ms })
    tx_p99_ms = Median @($subset | ForEach-Object { [double]$_.tx_p99_ms })
    commit_p50_ms = Median @($subset | ForEach-Object { [double]$_.commit_p50_ms })
    commit_p95_ms = Median @($subset | ForEach-Object { [double]$_.commit_p95_ms })
    commit_p99_ms = Median @($subset | ForEach-Object { [double]$_.commit_p99_ms })
    order_lock_p95_ms = Median @($subset | ForEach-Object { [double]$_.order_lock_p95_ms })
    rush_bucket_p95_ms = Median @($subset | ForEach-Object { [double]$_.rush_bucket_p95_ms })
    rush_bucket_p99_ms = Median @($subset | ForEach-Object { [double]$_.rush_bucket_p99_ms })
  }
}

$allRows | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $outputPath "VERIFY_RUNS.json")
$medians | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $outputPath "VERIFY_MEDIAN.json")

$md = New-Object System.Text.StringBuilder
[void]$md.AppendLine("# Order-query opt verify (split 70/30, cache ON)")
[void]$md.AppendLine("")
[void]$md.AppendLine(("Repeats={0}, Consumer={1}, HTTP/Worker={2}/{3}" -f $Repeats, $ConsumerWorkers, $HttpMaxOpenConns, $WorkerMaxOpenConns))
[void]$md.AppendLine("")
[void]$md.AppendLine("## Per-run")
[void]$md.AppendLine("| rate | rep | ach | P95 | cons | ready peak | ready slope | http in_use | http waitΔ | worker in_use | tx P50/P95 | commit P50/P95/P99 | lock P95 | rush P95/P99 |")
[void]$md.AppendLine("| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- | ---: | --- |")
foreach ($r in $allRows) {
  [void]$md.AppendLine(("| {0} | {1} | {2} | {3} | {4} | {5} | {6} | {7} | {8} | {9} | {10}/{11} | {12}/{13}/{14} | {15} | {16}/{17} |" -f `
    $r.rate, $r.rep, $r.achieved_req_rate, $r.http_p95_ms, $r.consumer_tx_per_sec, $r.order_ready_peak, $r.order_ready_slope_per_s, `
    $r.http_pool_in_use_peak, $r.http_pool_wait_count_delta, $r.worker_pool_in_use_peak, `
    $r.tx_p50_ms, $r.tx_p95_ms, $r.commit_p50_ms, $r.commit_p95_ms, $r.commit_p99_ms, `
    $r.order_lock_p95_ms, $r.rush_bucket_p95_ms, $r.rush_bucket_p99_ms))
}
[void]$md.AppendLine("")
[void]$md.AppendLine("## Median by rate")
[void]$md.AppendLine("| rate | ach | P95 | cons | ready peak | ready slope | http waitΔ | worker in_use | tx P50/P95/P99 | commit P50/P95/P99 | lock P95 | rush P95 |")
[void]$md.AppendLine("| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- | ---: | ---: |")
foreach ($r in $medians) {
  [void]$md.AppendLine(("| {0} | {1} | {2} | {3} | {4} | {5} | {6} | {7} | {8}/{9}/{10} | {11}/{12}/{13} | {14} | {15} |" -f `
    $r.rate, $r.achieved_req_rate, $r.http_p95_ms, $r.consumer_tx_per_sec, $r.order_ready_peak, $r.order_ready_slope_per_s, `
    $r.http_pool_wait_count_delta, $r.worker_pool_in_use_peak, `
    $r.tx_p50_ms, $r.tx_p95_ms, $r.tx_p99_ms, $r.commit_p50_ms, $r.commit_p95_ms, $r.commit_p99_ms, `
    $r.order_lock_p95_ms, $r.rush_bucket_p95_ms))
}
[void]$md.AppendLine("")
[void]$md.AppendLine("Reading: if commit P95 dominates tx P95 while worker wait=0, shared MySQL redo/IO is the Consumer ceiling.")
$text = $md.ToString()
[System.IO.File]::WriteAllText((Join-Path $outputPath "VERIFY.md"), $text, [System.Text.UTF8Encoding]::new($false))
Write-Host $text
Write-Host ("Verify: " + (Join-Path $outputPath "VERIFY.md")) -ForegroundColor Green

if (-not $KeepStack) {
  $baseCompose = Join-Path $repo "tests\integration\docker-compose.ticketing.yml"
  $capacityCompose = Join-Path $repo "tests\load\docker-compose.capacity.yml"
  & docker compose -p $Project -f $baseCompose -f $capacityCompose down -v --remove-orphans
}
