import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

export class ResourceSampler {
  constructor({
    containers = [],
    mysqlContainer = "",
    redisContainer = "",
    rabbitContainer = "",
    mysqlUser = process.env.MYSQL_SAMPLE_USER || "fuchang",
    mysqlPassword = process.env.MYSQL_SAMPLE_PASSWORD || "fuchang-it-mysql",
    redisPassword = process.env.REDIS_SAMPLE_PASSWORD || "fuchang-it-redis",
    intervalMs = 1000,
  } = {}) {
    this.containers = containers.filter(Boolean);
    this.mysqlContainer = mysqlContainer;
    this.redisContainer = redisContainer;
    this.rabbitContainer = rabbitContainer;
    this.mysqlUser = mysqlUser;
    this.mysqlPassword = mysqlPassword;
    this.redisPassword = redisPassword;
    this.intervalMs = intervalMs;
    this.samples = [];
    this.timer = null;
    this.sampling = false;
  }

  async start() {
    await this.sampleServices();
    this.timer = setInterval(() => {
      void this.sampleServices();
    }, this.intervalMs);
  }

  async stop() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    await this.sampleServices();
    return summarizeSamples(this.samples);
  }

  async sampleServices() {
    if (this.sampling) return;
    this.sampling = true;
    try {
      const at = new Date().toISOString();
      const [containers, mysql, redis, rabbit] = await Promise.all([
        this.sampleContainers(),
        this.sampleMySQL(),
        this.sampleRedis(),
        this.sampleRabbitMQ(),
      ]);
      for (const item of containers) {
        this.samples.push({ at, type: "container", ...item });
      }
      if (mysql) this.samples.push({ at, type: "mysql", ...mysql });
      if (redis) this.samples.push({ at, type: "redis", ...redis });
      if (rabbit) this.samples.push({ at, type: "rabbitmq", ...rabbit });
    } finally {
      this.sampling = false;
    }
  }

  async sampleContainers() {
    if (this.containers.length === 0) return [];
    try {
      const { stdout } = await execFileAsync(
        "docker",
        [
          "stats",
          "--no-stream",
          "--format",
          "{{json .}}",
          ...this.containers,
        ],
        { windowsHide: true, timeout: 10000 },
      );
      return stdout.trim().split(/\r?\n/).filter(Boolean).map((line) => {
        const item = JSON.parse(line);
        return {
          name: item.Name,
          cpu_pct: numberFromPercent(item.CPUPerc),
          memory_bytes: bytesFromDockerSize(String(item.MemUsage || "").split("/")[0]),
          pids: Number(item.PIDs || 0),
          block_io: item.BlockIO || "",
          net_io: item.NetIO || "",
        };
      });
    } catch {
      return [];
    }
  }

  async sampleMySQL() {
    if (!this.mysqlContainer) return null;
    const query = [
      "SHOW GLOBAL STATUS WHERE Variable_name IN (",
      "'Threads_connected','Threads_running','Innodb_row_lock_current_waits',",
      "'Innodb_row_lock_waits','Innodb_deadlocks','Connections','Aborted_connects');",
    ].join("");
    try {
      const { stdout } = await execFileAsync(
        "docker",
        [
          "exec",
          "-e",
          `MYSQL_PWD=${this.mysqlPassword}`,
          this.mysqlContainer,
          "mysql",
          "-N",
          "-B",
          `-u${this.mysqlUser}`,
          "-e",
          query,
        ],
        { windowsHide: true, timeout: 5000 },
      );
      return Object.fromEntries(
        stdout.trim().split(/\r?\n/).filter(Boolean).map((line) => {
          const [key, value] = line.split(/\s+/, 2);
          return [key, Number(value)];
        }),
      );
    } catch {
      return { sample_error: 1 };
    }
  }

  async sampleRedis() {
    if (!this.redisContainer) return null;
    try {
      const [info, latency] = await Promise.all([
        execFileAsync(
          "docker",
          [
            "exec",
            this.redisContainer,
            "redis-cli",
            "-a",
            this.redisPassword,
            "--no-auth-warning",
            "INFO",
            "stats",
          ],
          { windowsHide: true, timeout: 5000 },
        ),
        execFileAsync(
          "docker",
          [
            "exec",
            this.redisContainer,
            "redis-cli",
            "-a",
            this.redisPassword,
            "--no-auth-warning",
            "--latency",
            "-i",
            "0.1",
            "--raw",
          ],
          { windowsHide: true, timeout: 5000 },
        ),
      ]);
      const values = {};
      for (const line of info.stdout.split(/\r?\n/)) {
        const match = line.match(/^([^:#]+):([0-9.]+)\r?$/);
        if (match) values[match[1]] = Number(match[2]);
      }
      const [latencyMin, latencyMax, latencyAvg, latencySamples] = latency.stdout
        .trim()
        .split(/\s+/)
        .map(Number);
      return {
        instantaneous_ops_per_sec: values.instantaneous_ops_per_sec || 0,
        total_commands_processed: values.total_commands_processed || 0,
        rejected_connections: values.rejected_connections || 0,
        latency_min_ms: latencyMin || 0,
        latency_max_ms: latencyMax || 0,
        latency_avg_ms: latencyAvg || 0,
        latency_samples: latencySamples || 0,
      };
    } catch {
      return { sample_error: 1 };
    }
  }

  async sampleRabbitMQ() {
    if (!this.rabbitContainer) return null;
    try {
      const { stdout } = await execFileAsync(
        "docker",
        [
          "exec",
          this.rabbitContainer,
          "rabbitmqctl",
          "list_queues",
          "name",
          "messages_ready",
          "messages_unacknowledged",
          "consumers",
          "--formatter",
          "json",
        ],
        { windowsHide: true, timeout: 10000 },
      );
      const queues = JSON.parse(stdout);
      return {
        messages_ready: sum(queues, "messages_ready"),
        messages_unacknowledged: sum(queues, "messages_unacknowledged"),
        consumers: sum(queues, "consumers"),
        order_queue_ready: queueValue(queues, "fuchang.order.queue", "messages_ready"),
        order_queue_unacked: queueValue(
          queues,
          "fuchang.order.queue",
          "messages_unacknowledged",
        ),
        delay_queue_ready: queueValue(queues, "fuchang.order.delay", "messages_ready"),
      };
    } catch {
      return { sample_error: 1 };
    }
  }
}

export function summarizeSamples(samples) {
  const byType = (type) => samples.filter((item) => item.type === type);
  const containers = {};
  for (const item of byType("container")) {
    containers[item.name] ||= { cpu_pct: [], memory_bytes: [], pids: [] };
    containers[item.name].cpu_pct.push(item.cpu_pct);
    containers[item.name].memory_bytes.push(item.memory_bytes);
    containers[item.name].pids.push(item.pids);
  }
  const containerSummary = Object.fromEntries(
    Object.entries(containers).map(([name, values]) => [
      name,
      {
        cpu_pct_avg: average(values.cpu_pct),
        cpu_pct_max: maximum(values.cpu_pct),
        memory_bytes_max: maximum(values.memory_bytes),
        pids_max: maximum(values.pids),
      },
    ]),
  );

  const mysql = byType("mysql").filter((item) => !item.sample_error);
  const redis = byType("redis").filter((item) => !item.sample_error);
  const rabbit = byType("rabbitmq").filter((item) => !item.sample_error);

  return {
    sample_count: samples.length,
    containers: containerSummary,
    mysql: {
      threads_connected_max: maxField(mysql, "Threads_connected"),
      threads_running_max: maxField(mysql, "Threads_running"),
      row_lock_current_waits_max: maxField(mysql, "Innodb_row_lock_current_waits"),
      row_lock_waits_delta: deltaField(mysql, "Innodb_row_lock_waits"),
      deadlocks_delta: deltaField(mysql, "Innodb_deadlocks"),
      aborted_connects_delta: deltaField(mysql, "Aborted_connects"),
    },
    redis: {
      ops_per_sec_max: maxField(redis, "instantaneous_ops_per_sec"),
      latency_avg_ms_max: maxField(redis, "latency_avg_ms"),
      latency_max_ms: maxField(redis, "latency_max_ms"),
      commands_delta: deltaField(redis, "total_commands_processed"),
      rejected_connections_delta: deltaField(redis, "rejected_connections"),
    },
    rabbitmq: {
      messages_ready_max: maxField(rabbit, "messages_ready"),
      messages_unacknowledged_max: maxField(rabbit, "messages_unacknowledged"),
      order_queue_ready_max: maxField(rabbit, "order_queue_ready"),
      order_queue_unacked_max: maxField(rabbit, "order_queue_unacked"),
      delay_queue_ready_end: lastField(rabbit, "delay_queue_ready"),
      consumers_max: maxField(rabbit, "consumers"),
    },
    samples,
  };
}

function numberFromPercent(value) {
  return Number(String(value || "0").replace("%", "")) || 0;
}

function bytesFromDockerSize(value) {
  const match = String(value || "").trim().match(/^([0-9.]+)\s*([kKmMgGtT]?i?[bB])$/);
  if (!match) return 0;
  const units = {
    B: 1,
    KB: 1000,
    MB: 1000 ** 2,
    GB: 1000 ** 3,
    TB: 1000 ** 4,
    KiB: 1024,
    MiB: 1024 ** 2,
    GiB: 1024 ** 3,
    TiB: 1024 ** 4,
  };
  const normalized = match[2].replace(/^([kmgt])/, (c) => c.toUpperCase());
  return Number(match[1]) * (units[normalized] || 1);
}

function sum(rows, key) {
  return rows.reduce((total, row) => total + Number(row[key] || 0), 0);
}

function queueValue(rows, name, key) {
  return Number(rows.find((row) => row.name === name)?.[key] || 0);
}

function average(values) {
  if (values.length === 0) return 0;
  return Number((values.reduce((total, value) => total + value, 0) / values.length).toFixed(2));
}

function maximum(values) {
  return values.length === 0 ? 0 : Math.max(...values);
}

function maxField(rows, key) {
  return maximum(rows.map((row) => Number(row[key] || 0)));
}

function deltaField(rows, key) {
  if (rows.length < 2) return 0;
  return Number(rows.at(-1)[key] || 0) - Number(rows[0][key] || 0);
}

function lastField(rows, key) {
  return rows.length === 0 ? 0 : Number(rows.at(-1)[key] || 0);
}
