param(
  [string]$Project = "gofun-staircase-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [string]$OutputDir = "tests/load/results/$Project",
  [string]$BaselineImage = "gofun-perf-20261005-backend:baseline",
  [string]$CurrentImage = "gofun-scenarios-20261005-backend:latest"
)
$ErrorActionPreference = "Stop"
if ($Project -notmatch '^gofun-staircase-[a-z0-9-]+$') { throw "Use a dedicated staircase project" }
$outputPath = [IO.Path]::GetFullPath((Join-Path (Get-Location) $OutputDir))
New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
$env:CAPACITY_CONTAINER_PREFIX = $Project
$env:GOFUN_BACKEND_PORT = "18580"
$env:GOFUN_ELASTICSEARCH_PORT = "19602"
$env:GOFUN_MYSQL_PORT = "13327"
$env:GOFUN_REDIS_PORT = "16400"
$env:GOFUN_RABBITMQ_PORT = "26073"
$env:GOFUN_RABBITMQ_MANAGEMENT_PORT = "36073"
$compose = @("compose", "-p", $Project, "-f", "tests/integration/docker-compose.ticketing.yml", "-f", "tests/load/docker-compose.capacity.yml", "-f", "tests/load/docker-compose.audit.yml", "-f", (Join-Path $outputPath "image.json"))
$images = @{ baseline = $BaselineImage; current = $CurrentImage }
$consumers = @{ baseline = 6; current = 12 }
$manifest = [System.Collections.Generic.List[object]]::new()
foreach ($variant in @("baseline", "current")) {
  $imageId = docker image inspect $images[$variant] --format '{{.Id}}'
  if ($LASTEXITCODE -ne 0) { throw "Missing image: $($images[$variant])" }
  $manifest.Add(@{ variant=$variant; image=$images[$variant]; image_id=$imageId; consumers=$consumers[$variant] })
}
$manifest | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $outputPath "images.json")

function Set-Variant([string]$Variant) {
  @{ services=@{ backend=@{ image=$images[$Variant]; environment=@{
    ORDER_CONSUMER_WORKER_COUNT=[string]$consumers[$Variant]
    DELAYED_ORDER_TIMEOUT_MINUTES="60"
  } } } } | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 (Join-Path $outputPath "image.json")
}

Set-Variant "current"
docker @compose up -d --no-build --wait
if ($LASTEXITCODE -ne 0) { throw "Test stack startup failed" }
try {
  & ./tests/load/k6/run_full_chain_baseline.ps1 -Project $Project -Vus '200' -Rate 20 -Duration '5s' -DrainSeconds 300 -K6Users 10000 -FastPrepare -K6InDocker -K6Image grafana/k6:0.57.0 -KeepStack -ReuseStack -OrderConsumerWorkers 12 -ResourceCompose tests/load/docker-compose.audit.yml -OutputDir "$OutputDir/warmup"
  $index = 0
  foreach ($rate in @(100, 200, 300, 400, 200)) {
    $variants = if ($index % 2 -eq 0) { @("baseline", "current") } else { @("current", "baseline") }
    foreach ($variant in $variants) {
      $name = if ($index -eq 4) { "repeat-$variant-$rate" } else { "$variant-$rate" }
      Write-Host "STAGE $name input=$rate/s duration=120s" -ForegroundColor Cyan
      docker exec -e MYSQL_PWD=fuchang-it-mysql "${Project}-mysql-1" mysql -ufuchang fuchang_ticketing_it -e "UPDATE ticket_order SET expires_at=DATE_ADD(NOW(),INTERVAL 1 HOUR) WHERE status='pending_payment';"
      if ($LASTEXITCODE -ne 0) { throw "Fixture expiry update failed" }
      Set-Variant $variant
      docker @compose up -d --no-deps --no-build --wait backend
      if ($LASTEXITCODE -ne 0) { throw "Variant startup failed" }
      $runPath = "$OutputDir/$name"
      & ./tests/load/k6/run_full_chain_baseline.ps1 -Project $Project -Vus '200' -Rate $rate -Duration '120s' -DrainSeconds 300 -K6Users 10000 -FastPrepare -K6InDocker -K6Image grafana/k6:0.57.0 -KeepStack -ReuseStack -OrderConsumerWorkers $consumers[$variant] -ResourceCompose tests/load/docker-compose.audit.yml -OutputDir $runPath
      & node tests/load/k6/verify_full_chain.mjs $runPath $Project http://127.0.0.1:18580/api/v1
      if ($LASTEXITCODE -ne 0) { throw "$name inventory/order verification failed" }
      @{ variant=$variant; rate=$rate; duration_seconds=120; order=$index; consumers=$consumers[$variant]; repeat=($index -eq 4); image=(docker inspect "${Project}-backend-load" --format '{{.Image}}') } | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $runPath "variant.json")
      & node tests/load/k6/analyze_staircase.mjs $OutputDir
      if ($LASTEXITCODE -ne 0) { throw "Analysis failed" }
    }
    $index++
  }
} finally {
  docker @compose stop
}
