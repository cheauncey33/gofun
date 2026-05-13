package response

import (
	"WHU_Snack_GO/pkg/apperr"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Response struct {
	Code      int         `json:"code"`
	Message   string      `json:"msg"`
	Data      interface{} `json:"data,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
}

type PageData struct {
	List     interface{} `json:"list"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:      CodeSuccess,
		Message:   "ok",
		Data:      data,
		RequestID: getRequestID(c),
	})
}

func SuccessWithPage(c *gin.Context, list interface{}, total int64, page, pageSize int) {
	c.JSON(http.StatusOK, Response{
		Code:    CodeSuccess,
		Message: "ok",
		Data: PageData{
			List:     list,
			Total:    total,
			Page:     page,
			PageSize: pageSize,
		},
		RequestID: getRequestID(c),
	})
}

func Error(c *gin.Context, httpStatus int, bizCode int, message string) {
	c.AbortWithStatusJSON(httpStatus, Response{
		Code:      bizCode,
		Message:   message,
		RequestID: getRequestID(c),
	})
}

func ErrorWithAppErr(c *gin.Context, appErr *apperr.AppError) {
	httpStatus := mapAppErrToHTTP(appErr.Code)
	c.AbortWithStatusJSON(httpStatus, Response{
		Code:      appErr.Code,
		Message:   appErr.Message,
		RequestID: getRequestID(c),
	})
}

func getRequestID(c *gin.Context) string {
	if rid, ok := c.Get("request_id"); ok {
		return rid.(string)
	}
	return ""
}

func mapAppErrToHTTP(bizCode int) int {
	switch bizCode / 100 {
	case 400:
		return http.StatusBadRequest
	case 401:
		return http.StatusUnauthorized
	case 403:
		return http.StatusForbidden
	case 404:
		return http.StatusNotFound
	case 409:
		return http.StatusConflict
	case 429:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}
