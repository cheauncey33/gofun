package controller

import (
	"gofun/common"
	"gofun/pkg/response"
	"gofun/service"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type EventCommentController struct {
	service *service.EventCommentService
}

func NewEventCommentController(svc *service.EventCommentService) *EventCommentController {
	return &EventCommentController{service: svc}
}

type createCommentReq struct {
	Content string `json:"content" binding:"required"`
}

func (ctrl *EventCommentController) List(c *gin.Context) {
	eventID, ok := parseTicketID(c, "id")
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	viewerID, _ := common.GetUserID(c)
	list, total, err := ctrl.service.List(c.Request.Context(), eventID, page, pageSize, viewerID)
	if err != nil {
		writeCommentError(c, err)
		return
	}
	response.SuccessWithPage(c, list, total, page, pageSize)
}

func (ctrl *EventCommentController) Create(c *gin.Context) {
	eventID, ok := parseTicketID(c, "id")
	if !ok {
		return
	}
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	var req createCommentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	view, err := ctrl.service.Create(c.Request.Context(), eventID, userID, req.Content)
	if err != nil {
		writeCommentError(c, err)
		return
	}
	response.Success(c, view)
}

func (ctrl *EventCommentController) Delete(c *gin.Context) {
	commentID, ok := parseTicketID(c, "id")
	if !ok {
		return
	}
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	if err := ctrl.service.Delete(c.Request.Context(), commentID, userID); err != nil {
		writeCommentError(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}

func (ctrl *EventCommentController) Like(c *gin.Context) {
	commentID, ok := parseTicketID(c, "id")
	if !ok {
		return
	}
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	count, err := ctrl.service.Like(c.Request.Context(), commentID, userID)
	if err != nil {
		if errors.Is(err, service.ErrCommentAlreadyLiked) {
			response.Success(c, gin.H{"like_count": count, "already_liked": true})
			return
		}
		writeCommentError(c, err)
		return
	}
	response.Success(c, gin.H{"like_count": count, "already_liked": false})
}

func writeCommentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrCommentEventNotPublished),
		errors.Is(err, service.ErrCommentNotFound):
		response.Error(c, http.StatusNotFound, response.CodeNotFound, err.Error())
	case errors.Is(err, service.ErrCommentForbidden):
		response.Error(c, http.StatusForbidden, response.CodeForbidden, err.Error())
	case errors.Is(err, service.ErrCommentRateLimited):
		response.Error(c, http.StatusTooManyRequests, response.CodeTooManyRequests, err.Error())
	case errors.Is(err, service.ErrCommentInvalid):
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
	default:
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, err.Error())
	}
}
