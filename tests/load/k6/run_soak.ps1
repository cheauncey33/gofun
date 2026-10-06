param(
  [string]$Project = "gofun-soak-$(Get-Date -Format yyyyMMdd-HHmmss)",
  [string]$OutputDir = "tests/load/results/$Project"
)
$ErrorActionPreference = "Stop"
if ($Project -notmatch '^gofun-soak-[a-z0-9-]+$') { throw "Use a dedicated soak project" }
New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
$env:CAPACITY_CONTAINER_PREFIX = $Project
$env:GOFUN_BACKEND_PORT = "18580"
$env:GOFUN_ELASTICSEARCH_PORT = "19602"
$env:GOFUN_MYSQL_PORT = "13327"
$env:GOFUN_REDIS_PORT = "16400"
$env:GOFUN_RABBITMQ_PORT = "26073"
$env:GOFUN_RABBITMQ_MANAGEMENT_PORT = "36073"
$override = "$OutputDir/soak.json"
@{ services=@{
  backend=@{image="${Project}-backend";cpus=2;mem_limit="1g";environment=@{
    SERVER_DEMO_ACCOUNTS="true";DELAYED_ORDER_TIMEOUT_MINUTES="60"
    ORDER_CONSUMER_WORKER_COUNT="12";RATELIMIT_ORDER_RATE="100000";RATELIMIT_ORDER_BURST="100000"
  }}
  mysql=@{cpus=2;mem_limit="2g"};redis=@{cpus=1;mem_limit="512m"}
  rabbitmq=@{cpus=1;mem_limit="1g"};elasticsearch=@{cpus=1;mem_limit="1g"}
}} | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8 $override
$compose = @("compose", "-p", $Project, "-f", "tests/integration/docker-compose.ticketing.yml", "-f", "tests/load/docker-compose.capacity.yml", "-f", $override)
docker @compose up -d --build --wait
if ($LASTEXITCODE -ne 0) { throw "Test stack startup failed" }
try {
  docker inspect "${Project}-backend-load" --format '{{.Image}} {{json .Config.Env}}' | Set-Content -Encoding utf8 "$OutputDir/backend-settings.txt"
  $monitor = Start-Process -FilePath node -ArgumentList @("tests/load/k6/monitor_soak.mjs",$Project,$OutputDir) -WindowStyle Hidden -PassThru -RedirectStandardOutput "$OutputDir/monitor.stdout.log" -RedirectStandardError "$OutputDir/monitor.stderr.log"
  & ./tests/load/k6/run_full_chain_baseline.ps1 -Project $Project -Vus '400' -LoadProfile soak -Duration '1500s' -DrainSeconds 600 -K6Users 50000 -TotalQuota 500000 -FastPrepare -K6InDocker -K6Image grafana/k6:0.57.0 -KeepStack -ReuseStack -OrderConsumerWorkers 12 -ResourceCompose $override -OutputDir "$OutputDir/run"
  & node tests/load/k6/verify_full_chain.mjs "$OutputDir/run" $Project http://127.0.0.1:18580/api/v1
  if ($LASTEXITCODE -ne 0) { throw "Soak correctness verification failed" }
  & node tests/load/k6/analyze_soak.mjs $OutputDir
  if ($LASTEXITCODE -ne 0) { throw "Soak analysis failed" }
} finally {
  Set-Content -Path "$OutputDir/monitor.stop" -Value "stop"
  if ($monitor) { $monitor.WaitForExit(20000) | Out-Null }
  docker @compose stop
}
