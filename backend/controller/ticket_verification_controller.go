package controller

import (
	"errors"
	"gofun/pkg/response"
	"gofun/service"
	"net/http"

	"github.com/gin-gonic/gin"
)

type TicketVerificationController struct {
	service *service.TicketVerificationService
}

func NewTicketVerificationController(service *service.TicketVerificationService) *TicketVerificationController {
	return &TicketVerificationController{service: service}
}

func (ctrl *TicketVerificationController) Verify(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	var input struct {
		Credential string `json:"credential" binding:"required"`
		SessionID  int64  `json:"session_id,string"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "请提供票码")
		return
	}
	result, err := ctrl.service.Verify(
		c.Request.Context(),
		organizerID,
		userID,
		input.Credential,
		input.SessionID,
	)
	if err != nil {
		ctrl.writeError(c, err)
		return
	}
	response.Success(c, result)
}

func (ctrl *TicketVerificationController) ListRecords(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	page, pageSize := parseTicketPage(c)
	sessionID, ok := parseOptionalQueryID(c, "session_id")
	if !ok {
		return
	}
	view, err := ctrl.service.ListRecords(
		c.Request.Context(),
		organizerID,
		userID,
		sessionID,
		page,
		pageSize,
	)
	if err != nil {
		ctrl.writeError(c, err)
		return
	}
	response.Success(c, view)
}

func (ctrl *TicketVerificationController) writeError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrTicketAccessDenied) {
		response.Error(c, http.StatusForbidden, response.CodeForbidden, err.Error())
		return
	}
	response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "核销处理失败")
}
