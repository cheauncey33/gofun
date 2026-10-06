package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOrderRoutesShareTokenBucket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	limit := GlobalRateLimitMiddleware(0.001, 2)
	for _, path := range []string{"/orders", "/rush-sales/1/execute"} {
		r.POST(path, limit, func(c *gin.Context) { c.Status(http.StatusOK) })
	}
	for i, path := range []string{"/orders", "/rush-sales/1/execute", "/orders"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		want := http.StatusOK
		if i == 2 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("request %d: status=%d, want=%d", i, w.Code, want)
		}
	}
}
