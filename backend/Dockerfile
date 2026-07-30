FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/whu-snack-go .

FROM debian:bookworm-slim

WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/whu-snack-go /app/whu-snack-go
COPY backend/config/config.example.yaml /app/config/config.yaml

EXPOSE 8080
CMD ["/app/whu-snack-go", "-config", "/app/config/config.yaml"]
