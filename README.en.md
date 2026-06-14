# WHU Snack Go

WHU Snack Store System - A Campus Snack Delivery Platform Built with Go + Vue 3

## Project Overview

WHU Snack Go is a snack delivery platform designed specifically for Wuhan University, featuring a microservices architecture that supports core functionalities such as product browsing, shopping cart, order management, and flash sales. The system adopts a frontend-backend separation architecture: the backend is developed in Go, while the frontend is built using Vue 3 and Vite.

## Technology Stack

### Backend
- **Go 1.25** - Core language
- **Gin** - HTTP framework
- **GORM** - ORM framework
- **Redis** - Caching layer
- **RabbitMQ** - Message queue
- **MySQL** - Persistent storage
- **Snowflake** - Distributed ID generation

### Frontend
- **Vue 3** - Frontend framework
- **Vite** - Build tool
- **Element Plus** - UI component library
- **Pinia** - State management
- **Vue Router** - Routing management

### Infrastructure
- **Docker Compose** - Container orchestration
- **Prometheus** - Monitoring metrics
- **Grafana** - Visualization dashboard
- **Nginx** - Reverse proxy

## Core Features

### User Module
- User registration and login (JWT dual-token authentication)
- Personal information management
- Password modification
- Delivery address management

### Product Module
- Product listing (supports categorization, search, sorting)
- Product detail viewing
- Category management
- Inventory management (Redis caching + preheating)

### Order Module
- Shopping cart functionality
- Regular order placement (asynchronous via RabbitMQ)
- Order status transitions
- Order cancellation / refund request
- Automatic order cancellation upon timeout

### Flash Sale Module
- Flash sale event management
- Flash sale token mechanism
- Atomic inventory deduction via Lua scripts
- Purchase limits and overselling prevention
- Preheating and caching

### Admin Panel
- Dashboard statistics
- Product management (on/off shelf)
- Order management
- User management
- Flash sale event management

## Project Structure

```
whu_snack_go/
├── backend/
│   ├── common/          # Common components (JWT, Redis, RabbitMQ, rate limiting)
│   ├── config/          # Configuration management
│   ├── container/       # Dependency injection container
│   ├── controller/      # Controller layer
│   ├── models/          # Data models
│   ├── pkg/             # Common packages (error handling, logging, response)
│   ├── repository/      # Data access layer
│   ├── service/         # Business logic layer
│   ├── metrics/         # Prometheus monitoring
│   └── main.go          # Entry file
├── frontend/
│   ├── src/
│   │   ├── api/         # API requests
│   │   ├── components/  # Common components
│   │   ├── layouts/     # Layouts
│   │   ├── router/      # Routing configuration
│   │   ├── stores/      # Pinia state management
│   │   ├── views/       # Page views
│   │   └── main.js      # Entry file
├── deploy/              # Deployment configurations
├── monitoring/          # Monitoring configurations
├── tests/               # Test scripts
│   └── load/            # Load testing scripts
└── docker-compose.yml   # Container orchestration
```

## Quick Start

### Prerequisites

- Go 1.25+
- Node.js 18+
- Docker & Docker Compose
- MySQL 8.0+
- Redis 7.0+
- RabbitMQ 3.12+

### Backend Configuration

1. Copy the configuration file:
```bash
cp backend/config/config.example.yaml backend/config/config.yaml
```

2. Modify the configuration file to set up database, Redis, and RabbitMQ connection details.

3. Start the backend service:
```bash
cd backend
go run main.go -config config/config.yaml
```

### Frontend Configuration

1. Install dependencies:
```bash
cd frontend
npm install
```

2. Start the development server:
```bash
npm run dev
```

### Docker Compose Startup (Recommended)

```bash
# Start full service (backend + frontend + MySQL + Redis + RabbitMQ)
docker-compose up -d

# Start monitoring system
docker-compose -f monitoring/docker-compose.monitoring.yml up -d
```

## API Endpoints

### User Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | /api/v1/register | User registration |
| POST | /api/v1/login | User login |
| POST | /api/v1/refresh | Refresh token |
| GET | /api/v1/user/info | Get user information |
| PUT | /api/v1/user/info | Update user information |
| PUT | /api/v1/user/password | Change password |

### Product Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/products | Product list |
| GET | /api/v1/products/:id | Product details |
| GET | /api/v1/categories | Category list |

### Order Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | /api/v1/orders | Create order |
| GET | /api/v1/orders | Order list |
| GET | /api/v1/orders/:id | Order details |
| POST | /api/v1/orders/:id/cancel | Cancel order |
| POST | /api/v1/orders/:id/refund | Request refund |

### Flash Sale Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/seckill/activities | Flash sale list |
| GET | /api/v1/seckill/activities/:id | Flash sale details |
| POST | /api/v1/seckill/activities/:id/token | Get flash sale token |
| POST | /api/v1/seckill/activities/:id/execute | Execute flash sale |

### Admin Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/admin/dashboard | Dashboard |
| POST | /api/v1/admin/products | Create product |
| PUT | /api/v1/admin/products/:id | Update product |
| GET | /api/v1/admin/orders | Order list |
| PUT | /api/v1/admin/orders/:id/status | Update order status |

## Core Design

### High-Concurrency Handling

1. **Multi-level Caching**: L1 in-memory cache (Go Cache) + L2 Redis cache
2. **Rate Limiting**: Global rate limiting + IP-based rate limiting (token bucket algorithm)
3. **Flash Sale Optimization**: Preheating mechanism + Lua atomic operations + Token mechanism
4. **Asynchronous Ordering**: RabbitMQ message queue for traffic smoothing

### Data Consistency

1. **Inventory Compensation**: Scheduled task to reconcile Redis and MySQL inventory
2. **Idempotent Consumption**: Idempotency validation based on order ID
3. **Retry Mechanism**: Dead-letter queues + Delayed queues

### Observability

1. **Logging**: Zap structured logging
2. **Monitoring**: Prometheus metric collection
3. **Distributed Tracing**: Request ID propagation

## Load Testing

The project includes built-in load testing scripts supporting multiple scenarios:

```bash
# Install dependencies
cd tests/load
npm install

# Basic read load test
node read_baseline.mjs

# Flash sale spike load test
node seckill_spike.mjs

# Mixed read/write load test
node shopping_mix.mjs
```

## Deployment

Refer to [DEPLOYMENT.md](./DEPLOYMENT.md) for detailed instructions.

Recommended deployment architecture:
- Nginx (load balancer) → Go Backend (multiple instances)
- Frontend static assets served via CDN

## License

MIT License