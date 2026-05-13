package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRequestIDMiddleware_GeneratesID(t *testing.T) {
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.GET("/test", func(c *gin.Context) {
		rid, exists := c.Get("request_id")
		if !exists || rid == "" {
			c.String(http.StatusInternalServerError, "missing request_id")
			return
		}
		c.String(http.StatusOK, rid.(string))
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID header in response")
	}
}

func TestRequestIDMiddleware_UsesProvidedHeader(t *testing.T) {
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.GET("/test", func(c *gin.Context) {
		rid, _ := c.Get("request_id")
		c.String(http.StatusOK, rid.(string))
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "custom-rid-999")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Body.String() != "custom-rid-999" {
		t.Errorf("expected custom-rid-999, got %s", w.Body.String())
	}
	if w.Header().Get("X-Request-ID") != "custom-rid-999" {
		t.Error("expected response header to echo custom request ID")
	}
}

func TestRequestIDMiddleware_GeneratesUniqueIDs(t *testing.T) {
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.GET("/test", func(c *gin.Context) {
		rid, _ := c.Get("request_id")
		c.String(http.StatusOK, rid.(string))
	})

	ids := make(map[string]bool)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		id := w.Body.String()
		if ids[id] {
			t.Errorf("duplicate request ID generated: %s", id)
		}
		ids[id] = true
	}
}
