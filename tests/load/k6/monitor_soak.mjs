import { appendFile, access } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import { join, resolve } from "node:path";

const project=process.argv[2], root=resolve(process.argv[3]);
const docker=args=>execFileSync("docker",args,{encoding:"utf8",timeout:15000}).trim();
const parseMetrics=text=>Object.fromEntries(text.split("\n").flatMap(line=>{
  const match=line.match(/^([^#\s]+)\s+([\d.eE+-]+)$/);
  return match?[[match[1],Number(match[2])]]:[];
}));
let count=0;
for (;;) {
  try { await access(join(root,"monitor.stop")); break; } catch {}
  const sample={timestamp:new Date().toISOString()};
  try {
    const response=await fetch("http://127.0.0.1:18580/metrics",{signal:AbortSignal.timeout(5000)});
    if (!response.ok) throw new Error(`metrics HTTP ${response.status}`);
    sample.prometheus=parseMetrics(await response.text());
    if (count%6===0) {
      sample.containers=docker(["stats","--no-stream","--format","{{json .}}",`${project}-backend-load`,`${project}-mysql-1`,`${project}-redis-1`,`${project}-rabbitmq-1`]).split(/\r?\n/).map(JSON.parse);
      sample.mysql_io=docker(["exec",`${project}-mysql-1`,"sh","-c","cat /proc/pressure/io /proc/pressure/memory /sys/fs/cgroup/io.stat /sys/fs/cgroup/cpu.stat /sys/fs/cgroup/memory.current"]);
      sample.mysql_status=docker(["exec","-e","MYSQL_PWD=fuchang-it-mysql",`${project}-mysql-1`,"mysql","-ufuchang","-N","-B","-e","SHOW GLOBAL STATUS WHERE Variable_name IN ('Innodb_log_waits','Innodb_os_log_written','Innodb_data_fsyncs','Innodb_buffer_pool_wait_free','Innodb_buffer_pool_reads','Innodb_buffer_pool_read_requests','Threads_running','Threads_connected','Innodb_row_lock_time','Innodb_row_lock_waits');"]);
    }
  } catch (error) { sample.error=String(error); }
  await appendFile(join(root,"live.ndjson"),JSON.stringify(sample)+"\n");
  count++;
  await new Promise(resolve=>setTimeout(resolve,10000));
}
