package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("request_id", "test-rid-123")

	Success(c, gin.H{"name": "test"})

	assert.Equal(t, http.StatusOK, w.Code)

	var resp Response
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, CodeSuccess, resp.Code)
	assert.Equal(t, "ok", resp.Message)
	assert.Equal(t, "test-rid-123", resp.RequestID)

	data, ok := resp.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "test", data["name"])
}

func TestSuccessWithPage(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("request_id", "rid-page")

	list := []string{"a", "b", "c"}
	SuccessWithPage(c, list, 100, 2, 10)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp Response
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, CodeSuccess, resp.Code)

	// PageData is embedded in Data
	dataJSON, _ := json.Marshal(resp.Data)
	var pd PageData
	json.Unmarshal(dataJSON, &pd)
	assert.Equal(t, int64(100), pd.Total)
	assert.Equal(t, 2, pd.Page)
	assert.Equal(t, 10, pd.PageSize)
}

func TestError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("request_id", "rid-err")

	Error(c, http.StatusBadRequest, CodeBadRequest, "参数错误")

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp Response
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, CodeBadRequest, resp.Code)
	assert.Equal(t, "参数错误", resp.Message)
	assert.Equal(t, "rid-err", resp.RequestID)
}

func TestError_NoRequestID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	Error(c, http.StatusInternalServerError, CodeInternalError, "内部错误")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp Response
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "", resp.RequestID)
}

func TestSuccess_NilData(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	Success(c, nil)

	var resp Response
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, CodeSuccess, resp.Code)
	assert.Nil(t, resp.Data)
}
