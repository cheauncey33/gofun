package response

const (
	CodeSuccess = 0

	// 客户端错误 4xxxx
	CodeBadRequest   = 40000
	CodeUnauthorized = 40100
	CodeTokenExpired = 40101
	CodeTokenInvalid = 40102
	CodeForbidden    = 40300
	CodeNotFound     = 40400
	CodeUserNotFound = 40401
	CodeOrderNotFound = 40403
	CodeConflict     = 40900
	CodeUserExists   = 40901
	CodeTooManyRequests = 42900

	// 服务端错误 5xxxx
	CodeInternalError = 50000
	CodeDBError       = 50001
	CodeRedisError    = 50002
	CodeMQError       = 50003

	// 业务错误 6xxxx
	CodeInsufficientStock  = 60001
	CodeInsufficientBalance = 60002
	CodeOrderCreateFailed  = 60003
	CodeInvalidOrderStatus = 60009
)

var codeMessages = map[int]string{
	CodeSuccess:           "ok",
	CodeBadRequest:        "参数错误",
	CodeUnauthorized:      "未登录",
	CodeTokenExpired:      "token已过期",
	CodeTokenInvalid:      "token无效",
	CodeForbidden:         "权限不足",
	CodeNotFound:          "资源不存在",
	CodeUserNotFound:      "用户不存在",
	CodeOrderNotFound:     "订单不存在",
	CodeConflict:          "资源冲突",
	CodeUserExists:        "用户名已存在",
	CodeTooManyRequests:   "请求过于频繁",
	CodeInternalError:     "服务器内部错误",
	CodeDBError:           "数据库错误",
	CodeRedisError:        "缓存服务错误",
	CodeMQError:           "消息队列错误",
	CodeInsufficientStock: "库存不足",
	CodeInsufficientBalance: "余额不足",
	CodeOrderCreateFailed: "订单创建失败",
	CodeInvalidOrderStatus:  "订单状态不允许此操作",
}

func GetMessage(code int) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "未知错误"
}
