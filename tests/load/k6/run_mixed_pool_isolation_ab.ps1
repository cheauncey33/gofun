param(
  [string]$Rates = "800,1000,1200",
  [string]$Duration = "60s",
  [int]$ConsumerWorkers = 8,
  [switch]$SkipLogBin,
  [string]$Project = "gofun-csweep",
  [string]$OutputDir = "tests/load/results/mixed-pool-isolation-ab-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [switch]$KeepStack
)

$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$outputPath = Join-Path $repo $OutputDir
New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
$profileScript = Join-Path $PSScriptRoot "run_mixed_resource_profile.ps1"

$arms = @(
  @{ name = "shared-100"; HttpMaxOpenConns = 100; WorkerMaxOpenConns = 0 },
  @{ name = "split-70-30"; HttpMaxOpenConns = 70; WorkerMaxOpenConns = 30 }
)

$compare = @()
foreach ($arm in $arms) {
  Write-Host ("===== ARM {0} HTTP={1} worker={2} =====" -f $arm.name, $arm.HttpMaxOpenConns, $arm.WorkerMaxOpenConns) -ForegroundColor Cyan
  $armDir = Join-Path $OutputDir $arm.name
  $args = @(
    "-Rates", $Rates,
    "-Duration", $Duration,
    "-ConsumerWorkers", $ConsumerWorkers,
    "-HttpMaxOpenConns", $arm.HttpMaxOpenConns,
    "-WorkerMaxOpenConns", $arm.WorkerMaxOpenConns,
    "-Project", $Project,
    "-OutputDir", $armDir,
    "-KeepStack"
  )
  if ($SkipLogBin) { $args += "-SkipLogBin" }
  & powershell -NoProfile -ExecutionPolicy Bypass -File $profileScript @args
  if ($LASTEXITCODE -ne 0) { throw "profile failed for $($arm.name)" }

  $summaryPath = Join-Path $repo (Join-Path $armDir "profile-summary.json")
  $rows = Get-Content $summaryPath -Raw -Encoding utf8 | ConvertFrom-Json
  foreach ($r in @($rows)) {
    $compare += [pscustomobject]@{
      arm = $arm.name
      http_max_open = $arm.HttpMaxOpenConns
      worker_max_open = $arm.WorkerMaxOpenConns
      target_rate = $r.target_rate
      achieved_req_rate = $r.achieved_req_rate
      http_p95_ms = $r.http_p95_ms
      http_p99_ms = $r.http_p99_ms
      consumer_tx_per_sec = $r.consumer_tx_per_sec
      order_ready_peak = $r.order_ready_peak
      order_ready_slope_per_s = $r.order_ready_slope_per_s
      http_pool_in_use_peak = $r.http_pool_in_use_peak
      http_pool_wait_count_delta = $r.http_pool_wait_count_delta
      worker_pool_in_use_peak = $r.worker_pool_in_use_peak
      worker_pool_wait_count_delta = $r.worker_pool_wait_count_delta
    }
  }
}

$compare | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $outputPath "AB_COMPARE.json")

$md = New-Object System.Text.StringBuilder
[void]$md.AppendLine("# Pool isolation A/B (shared-100 vs split-70-30)")
[void]$md.AppendLine("")
[void]$md.AppendLine("Total connections kept ≈100. Catalog cache ON. Order-query optimization NOT included.")
[void]$md.AppendLine("")
[void]$md.AppendLine("| arm | rate | ach | P95 | P99 | cons tx/s | ready peak | ready slope/s | http in_use | http waitΔ | worker in_use | worker waitΔ |")
[void]$md.AppendLine("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
foreach ($r in $compare) {
  [void]$md.AppendLine(("| {0} | {1} | {2} | {3} | {4} | {5} | {6} | {7} | {8} | {9} | {10} | {11} |" -f `
    $r.arm, $r.target_rate, $r.achieved_req_rate, $r.http_p95_ms, $r.http_p99_ms, $r.consumer_tx_per_sec, `
    $r.order_ready_peak, $r.order_ready_slope_per_s, $r.http_pool_in_use_peak, $r.http_pool_wait_count_delta, `
    $r.worker_pool_in_use_peak, $r.worker_pool_wait_count_delta))
}
[void]$md.AppendLine("")
[void]$md.AppendLine("## Success criteria @ 1000 QPS")
[void]$md.AppendLine("- Consumer recovers toward 90~120 tx/s")
[void]$md.AppendLine("- HTTP pool wait drops vs shared-100")
[void]$md.AppendLine("- MQ ready slope slows materially")
$text = $md.ToString()
[System.IO.File]::WriteAllText((Join-Path $outputPath "AB_COMPARE.md"), $text, [System.Text.UTF8Encoding]::new($false))
Write-Host $text
Write-Host ("AB compare: " + (Join-Path $outputPath "AB_COMPARE.md")) -ForegroundColor Green

if (-not $KeepStack) {
  $baseCompose = Join-Path $repo "tests\integration\docker-compose.ticketing.yml"
  $capacityCompose = Join-Path $repo "tests\load\docker-compose.capacity.yml"
  & docker compose -p $Project -f $baseCompose -f $capacityCompose down -v --remove-orphans
}
