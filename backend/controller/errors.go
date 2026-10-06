package controller

import (
	"log"
	"net/http"

	"gofun/pkg/response"
	"gofun/service"

	"github.com/gin-gonic/gin"
)

// 本文件是 controller 层唯一的错误出口。
//
// 契约（新增接口时不要绕过这里手写 response.Error）：
//  1. 响应体的 message 只能取自 service.ErrorSpec 的用户文案或本文件的兜底文案；
//     err.Error() 携带 SQL 细节、字段名、内部表名等信息，只允许写服务端日志。
//  2. 未登记的错误会触发告警日志与 Prometheus 计数，便于发现翻译缺失。
//  3. 参数绑定失败走 badRequest，不拼接原始错误，避免把 binder 的结构信息透出去。

// writeError 统一渲染 service 层返回的错误。
func writeError(c *gin.Context, context string, err error, fallbackMessage string) {
	if err == nil {
		return
	}
	log.Printf("[%s] request failed: method=%s path=%s err=%v",
		context, c.Request.Method, c.Request.URL.Path, err)
	if httpStatus, code, message, ok := service.ErrorSpec(err); ok {
		response.Error(c, httpStatus, code, message)
		return
	}
	service.NoteUnmappedError(context, err)
	response.Error(c, http.StatusInternalServerError, response.CodeInternalError, fallbackMessage)
}

func writeTicketOrderError(c *gin.Context, err error) {
	writeError(c, "ticket_order", err, "票务订单处理失败，请稍后再试")
}

func writeTicketCatalogError(c *gin.Context, err error) {
	writeError(c, "ticket_catalog", err, "票务服务暂时不可用")
}

func writeCommentError(c *gin.Context, err error) {
	writeError(c, "event_comment", err, "评论服务暂时不可用")
}

func writeVerificationError(c *gin.Context, err error) {
	writeError(c, "ticket_verification", err, "核销处理失败")
}

func writeRushSaleError(c *gin.Context, err error) {
	writeError(c, "rush_sale", err, "限时开票暂时不可用")
}

func writeUserError(c *gin.Context, err error) {
	writeError(c, "user", err, "用户服务暂时不可用")
}

// badRequest 渲染请求参数类错误：固定文案回给调用方，真实原因只进日志。
func badRequest(c *gin.Context, err error, message string) {
	if err != nil {
		log.Printf("bad request: method=%s path=%s err=%v", c.Request.Method, c.Request.URL.Path, err)
	}
	response.Error(c, http.StatusBadRequest, response.CodeBadRequest, message)
}

// writeBizError 用于那些没有哨兵错误、但需要指定业务码的场景。
// 同样遵循「文案写死、原因进日志」的规则。
func writeBizError(c *gin.Context, err error, httpStatus, code int, message string) {
	if err != nil {
		log.Printf("biz error: method=%s path=%s err=%v", c.Request.Method, c.Request.URL.Path, err)
	}
	response.Error(c, httpStatus, code, message)
}
