# WHU Snack Go Deployment

This deployment runs the Vue frontend, Go API, MySQL, Redis, and RabbitMQ on one Linux server with Docker Compose.

## Fast Server Choice

Use a small Ubuntu server first:

- Minimum for demo: 2 vCPU, 2 GB RAM, 40 GB disk.
- Better for RabbitMQ + MySQL + Redis together: 2 vCPU, 4 GB RAM.
- Open inbound ports: 22, 80, 443.

For the fastest mainland China setup, buy a lightweight application server from Aliyun, Tencent Cloud, Huawei Cloud, or JD Cloud in a nearby region. If you do not need mainland China access or ICP filing, an overseas VPS avoids ICP filing and is usually faster to publish publicly.

## Prepare The Server

Install Docker on Ubuntu:

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl git
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo tee /etc/apt/keyrings/docker.asc >/dev/null
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | sudo tee /etc/apt/sources.list.d/docker.list >/dev/null
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
```

## Deploy

Clone or upload this repository, then create the environment file:

```bash
cp deploy/env.example .env
nano .env
```

Set strong random values in `.env`. If you already have a domain, set `APP_ORIGIN=https://your-domain.com`. Without a domain, set it to `http://your-server-ip`.

Start the stack:

```bash
docker compose up -d --build
docker compose ps
```

View logs:

```bash
docker compose logs -f backend
```

The site is available at:

- `http://your-server-ip`
- `http://your-domain.com` after DNS is configured

Default admin user created by the app:

- username: `admin`
- password: `admin123`

Change this immediately after first login or update the seed logic before exposing the site.

## HTTPS

The fastest HTTPS option is Cloudflare proxy:

1. Point your domain DNS A record to the server IP.
2. Enable Cloudflare proxy for the record.
3. Set SSL/TLS mode to Full.

For direct HTTPS on the server, add Caddy or Certbot/Nginx in front of this Compose stack.

## Update

```bash
git pull
docker compose up -d --build
```

## Backup

Back up Docker volumes before risky changes:

```bash
docker run --rm -v whu_snack_go_mysql_data:/volume -v "$PWD:/backup" alpine tar czf /backup/mysql_data.tgz -C /volume .
```
