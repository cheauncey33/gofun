package controller

import (
	"gofun/pkg/response"
	"gofun/service"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type RushSaleController struct {
	service *service.RushSaleService
}

func NewRushSaleController(service *service.RushSaleService) *RushSaleController {
	return &RushSaleController{service: service}
}

func (ctrl *RushSaleController) ListCampaigns(c *gin.Context) {
	ctx := c.Request.Context()
	var campaigns []service.RushSaleCampaignView
	var err error
	if raw := strings.TrimSpace(c.Query("event_id")); raw != "" {
		eventID, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || eventID <= 0 {
			response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "活动无效")
			return
		}
		campaigns, err = ctrl.service.ListCampaignsForEvent(ctx, eventID)
	} else {
		campaigns, err = ctrl.service.ListCampaigns(ctx)
	}
	if err != nil {
		writeTicketOrderError(c, err)
		return
	}
	response.Success(c, campaigns)
}

func (ctrl *RushSaleController) CreateCampaign(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	var input service.CreateRushSaleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	campaign, err := ctrl.service.CreateCampaign(
		c.Request.Context(), userID, organizerID, input,
	)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, campaign)
}

func (ctrl *RushSaleController) Execute(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	campaignID, ok := parseTicketID(c, "id")
	if !ok {
		return
	}
	idempotencyKey := c.GetHeader("X-Idempotency-Key")
	if idempotencyKey == "" {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "缺少 X-Idempotency-Key")
		return
	}
	var input struct {
		Quantity int `json:"quantity" binding:"required"`
		service.PurchaseInfoInput
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	requestID, _ := c.Get("request_id")
	receipt, err := ctrl.service.Execute(
		c.Request.Context(),
		userID,
		campaignID,
		idempotencyKey,
		stringifyRequestID(requestID),
		input.Quantity,
		input.PurchaseInfoInput,
	)
	if err != nil {
		writeTicketOrderError(c, err)
		return
	}
	response.Success(c, receipt)
}
