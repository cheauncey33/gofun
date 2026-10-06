param(
  [string]$Project = "gofun-peak-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [string]$OutputDir = "tests/load/results/$Project"
)
$ErrorActionPreference = "Stop"
if ($Project -notmatch '^gofun-peak-[a-z0-9-]+$') { throw "Use a dedicated peak project" }
New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
$env:CAPACITY_CONTAINER_PREFIX = $Project
$env:GOFUN_BACKEND_PORT = "18580"
$env:GOFUN_ELASTICSEARCH_PORT = "19602"
$env:GOFUN_MYSQL_PORT = "13327"
$env:GOFUN_REDIS_PORT = "16400"
$env:GOFUN_RABBITMQ_PORT = "26073"
$env:GOFUN_RABBITMQ_MANAGEMENT_PORT = "36073"
$override = "$OutputDir/peak.json"
$compose = @("compose", "-p", $Project, "-f", "tests/integration/docker-compose.ticketing.yml", "-f", "tests/load/docker-compose.capacity.yml", "-f", $override)
function Set-Profile([string]$Limit) {
  @{ services=@{
    backend=@{image="${Project}-backend"; cpus=2; mem_limit="1g"; environment=@{
      SERVER_DEMO_ACCOUNTS="true"; DELAYED_ORDER_TIMEOUT_MINUTES="60"
      ORDER_CONSUMER_WORKER_COUNT="12"; RATELIMIT_ORDER_RATE=$Limit; RATELIMIT_ORDER_BURST="50"
    }}
    mysql=@{cpus=2;mem_limit="2g"};redis=@{cpus=1;mem_limit="512m"}
    rabbitmq=@{cpus=1;mem_limit="1g"};elasticsearch=@{cpus=1;mem_limit="1g"}
  }} | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 $override
}
Set-Profile "150"
docker @compose up -d --build --wait
if ($LASTEXITCODE -ne 0) { throw "Test stack startup failed" }
try {
  foreach ($mode in @("limited", "buffered")) {
    if ($mode -eq "buffered") {
      Set-Profile "100000"
      docker @compose up -d --no-deps --no-build --wait backend
      if ($LASTEXITCODE -ne 0) { throw "Backend restart failed" }
    }
    $runPath = "$OutputDir/$mode"
    & ./tests/load/k6/run_full_chain_baseline.ps1 -Project $Project -Vus '400' -LoadProfile peak -Duration '190s' -DrainSeconds 300 -K6Users 10000 -FastPrepare -K6InDocker -K6Image grafana/k6:0.57.0 -KeepStack -ReuseStack -OrderConsumerWorkers 12 -ResourceCompose $override -OutputDir $runPath
    & node tests/load/k6/verify_full_chain.mjs $runPath $Project http://127.0.0.1:18580/api/v1
    if ($LASTEXITCODE -ne 0) { throw "$mode correctness verification failed" }
    docker inspect "${Project}-backend-load" --format '{{.Image}} {{json .Config.Env}}' | Set-Content -Encoding utf8 "$runPath/backend-settings.txt"
  }
  & node tests/load/k6/analyze_peak_recovery.mjs $OutputDir
  if ($LASTEXITCODE -ne 0) { throw "Peak recovery analysis failed" }
} finally {
  docker @compose stop
}
