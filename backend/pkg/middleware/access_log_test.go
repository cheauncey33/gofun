package middleware

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gofun/pkg/logger"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccessLogCorrelationAndRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zapcore.InfoLevel)
	previous := logger.Log
	logger.Log = zap.New(core)
	defer func() { logger.Log = previous }()
	r := gin.New()
	r.Use(RequestIDMiddleware())
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(trace.ContextWithSpanContext(c.Request.Context(), sc))
		c.Next()
	})
	r.Use(AccessLogMiddleware(), RecoveryMiddleware())
	r.GET("/orders/:id", func(c *gin.Context) { c.Set("user_id", int64(42)); c.Status(http.StatusOK) })
	r.GET("/panic", func(c *gin.Context) { panic("test panic") })
	for _, path := range []string{"/orders/123?secret=hidden", "/panic"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-Request-ID", "test-request")
		req.Header.Set("Authorization", "Bearer hidden-token")
		response := httptest.NewRecorder()
		r.ServeHTTP(response, req)
		expected := 200
		if path == "/panic" {
			expected = 500
		}
		if response.Code != expected {
			t.Fatalf("status=%d want=%d", response.Code, expected)
		}
	}
	entries := logs.All()
	if len(entries) != 3 {
		t.Fatalf("entries=%d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["request_id"] != "test-request" || fields["trace_id"] != sc.TraceID().String() || fields["user_id"] != int64(42) {
		t.Fatalf("missing correlation: %v", fields)
	}
	if fields["path"] != "/orders/123" || fields["route"] != "/orders/:id" {
		t.Fatalf("unexpected path: %v", fields)
	}
	if entries[2].Level != zapcore.ErrorLevel || entries[2].ContextMap()["status"] != int64(500) {
		t.Fatal("panic access log must report 500")
	}
	for _, entry := range entries {
		for key := range entry.ContextMap() {
			if key == "headers" || key == "body" || key == "query" {
				t.Fatalf("sensitive field: %s", key)
			}
		}
	}
}
