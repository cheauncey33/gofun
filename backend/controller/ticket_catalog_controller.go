package controller

import (
	"errors"
	"gofun/common"
	"gofun/pkg/response"
	"gofun/service"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type TicketCatalogController struct {
	service *service.TicketCatalogService
	orders  *service.TicketOrderService
}

func NewTicketCatalogController(
	service *service.TicketCatalogService,
	orders *service.TicketOrderService,
) *TicketCatalogController {
	return &TicketCatalogController{service: service, orders: orders}
}

func (ctrl *TicketCatalogController) ListPublishedEvents(c *gin.Context) {
	var query service.TicketCatalogListQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "查询参数错误")
		return
	}
	query.Normalize()
	events, total, err := ctrl.service.ListPublishedEvents(c.Request.Context(), query)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.SuccessWithPage(c, events, total, query.Page, query.PageSize)
}

func (ctrl *TicketCatalogController) GetCatalogMeta(c *gin.Context) {
	meta, err := ctrl.service.GetCatalogMeta(c.Request.Context())
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, meta)
}

func (ctrl *TicketCatalogController) GetPublishedEvent(c *gin.Context) {
	eventID, ok := parseTicketID(c, "id")
	if !ok {
		return
	}
	event, err := ctrl.service.GetPublishedEvent(c.Request.Context(), eventID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, event)
}

func (ctrl *TicketCatalogController) AdminCreateOrganizer(c *gin.Context) {
	var input service.CreateOrganizerInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	organizer, err := ctrl.service.CreateOrganizer(c.Request.Context(), input)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, organizer)
}

func (ctrl *TicketCatalogController) AdminListOrganizers(c *gin.Context) {
	page, pageSize := parseTicketPage(c)
	organizers, total, err := ctrl.service.ListAdminOrganizers(
		c.Request.Context(), c.Query("audit_status"), page, pageSize,
	)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.SuccessWithPage(c, organizers, total, page, pageSize)
}

func (ctrl *TicketCatalogController) ApplyOrganizer(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	var input service.ApplyOrganizerInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	organizer, err := ctrl.service.ApplyOrganizer(c.Request.Context(), userID, input)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, organizer)
}

func (ctrl *TicketCatalogController) AdminApproveOrganizer(c *gin.Context) {
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	organizer, err := ctrl.service.ApproveOrganizer(c.Request.Context(), organizerID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, organizer)
}

func (ctrl *TicketCatalogController) AdminRejectOrganizer(c *gin.Context) {
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	var input service.ReviewDecisionInput
	_ = c.ShouldBindJSON(&input)
	organizer, err := ctrl.service.RejectOrganizer(c.Request.Context(), organizerID, input.Note)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, organizer)
}

func (ctrl *TicketCatalogController) AdminListPendingEvents(c *gin.Context) {
	page, pageSize := parseTicketPage(c)
	events, total, err := ctrl.service.ListPendingEventReviews(c.Request.Context(), page, pageSize)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.SuccessWithPage(c, events, total, page, pageSize)
}

func (ctrl *TicketCatalogController) AdminApproveEvent(c *gin.Context) {
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	event, err := ctrl.service.AdminApproveEvent(c.Request.Context(), eventID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, event)
}

func (ctrl *TicketCatalogController) AdminRejectEvent(c *gin.Context) {
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	var input service.ReviewDecisionInput
	_ = c.ShouldBindJSON(&input)
	event, err := ctrl.service.AdminRejectEvent(c.Request.Context(), eventID, input.Note)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, event)
}

func (ctrl *TicketCatalogController) AdminPlatformOverview(c *gin.Context) {
	overview, err := ctrl.service.GetAdminPlatformOverview(c.Request.Context())
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, overview)
}

func (ctrl *TicketCatalogController) ListMyOrganizers(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizers, err := ctrl.service.ListMyOrganizers(c.Request.Context(), userID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, organizers)
}

func (ctrl *TicketCatalogController) CreateVenue(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	var input service.CreateVenueInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	venue, err := ctrl.service.CreateVenue(c.Request.Context(), userID, organizerID, input)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, venue)
}

func (ctrl *TicketCatalogController) CreateHall(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	venueID, ok := parseTicketID(c, "venue_id")
	if !ok {
		return
	}
	var input service.CreateHallInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	hall, err := ctrl.service.CreateHall(c.Request.Context(), userID, organizerID, venueID, input)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, hall)
}

func (ctrl *TicketCatalogController) ListHalls(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	venueID, ok := parseTicketID(c, "venue_id")
	if !ok {
		return
	}
	halls, err := ctrl.service.ListHalls(c.Request.Context(), userID, organizerID, venueID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, halls)
}

func (ctrl *TicketCatalogController) ListHallLayouts(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	hallID, ok := parseTicketID(c, "hall_id")
	if !ok {
		return
	}
	layouts, err := ctrl.service.ListHallLayouts(c.Request.Context(), userID, organizerID, hallID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, layouts)
}

func (ctrl *TicketCatalogController) CreateHallLayout(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	hallID, ok := parseTicketID(c, "hall_id")
	if !ok {
		return
	}
	var input service.HallLayoutInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	layout, err := ctrl.service.CreateHallLayout(c.Request.Context(), userID, organizerID, hallID, input)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, layout)
}

func (ctrl *TicketCatalogController) UpdateHallLayout(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	layoutID, ok := parseTicketID(c, "layout_id")
	if !ok {
		return
	}
	var input service.HallLayoutInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	layout, err := ctrl.service.UpdateHallLayout(c.Request.Context(), userID, organizerID, layoutID, input)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, layout)
}

func (ctrl *TicketCatalogController) PublishHallLayout(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	layoutID, ok := parseTicketID(c, "layout_id")
	if !ok {
		return
	}
	layout, err := ctrl.service.PublishHallLayout(c.Request.Context(), userID, organizerID, layoutID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, layout)
}

func (ctrl *TicketCatalogController) ConfigureSessionSeatMap(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	sessionID, ok := parseTicketID(c, "session_id")
	if !ok {
		return
	}
	var input service.SessionSeatMapInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	rows, err := ctrl.service.ConfigureSessionSeatMap(c.Request.Context(), userID, organizerID, sessionID, input)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, rows)
}

func (ctrl *TicketCatalogController) ListVenues(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	venues, err := ctrl.service.ListVenues(c.Request.Context(), userID, organizerID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, venues)
}

func (ctrl *TicketCatalogController) CreateEvent(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	var input service.CreateEventInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	event, err := ctrl.service.CreateEvent(c.Request.Context(), userID, organizerID, input)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, event)
}

func (ctrl *TicketCatalogController) UpdateEvent(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	var input service.CreateEventInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	event, err := ctrl.service.UpdateEvent(c.Request.Context(), userID, organizerID, eventID, input)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, event)
}

func (ctrl *TicketCatalogController) ListOrganizerEvents(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	var query service.TicketCatalogListQuery
	_ = c.ShouldBindQuery(&query)
	events, total, err := ctrl.service.ListOrganizerEvents(
		c.Request.Context(), userID, organizerID, query,
	)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	query.Normalize()
	response.SuccessWithPage(c, events, total, query.Page, query.PageSize)
}

func (ctrl *TicketCatalogController) GetOrganizerOverview(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	eventID, ok := parseOptionalQueryID(c, "event_id")
	if !ok {
		return
	}
	sessionID, ok := parseOptionalQueryID(c, "session_id")
	if !ok {
		return
	}
	overview, err := ctrl.service.GetOrganizerOverview(
		c.Request.Context(), userID, organizerID, eventID, sessionID,
	)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, overview)
}

func (ctrl *TicketCatalogController) TrackFunnelVisits(c *gin.Context) {
	var input service.TrackFunnelInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "上报参数错误")
		return
	}
	userID, _ := common.GetUserID(c)
	if err := ctrl.service.TrackFunnelVisits(
		c.Request.Context(), userID, c.ClientIP(), c.GetHeader("User-Agent"), input,
	); err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, gin.H{})
}

func (ctrl *TicketCatalogController) GetOrganizerFunnel(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	eventID, ok := parseOptionalQueryID(c, "event_id")
	if !ok {
		return
	}
	funnel, err := ctrl.service.GetOrganizerFunnel(
		c.Request.Context(), userID, organizerID, eventID, days,
	)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, funnel)
}

func (ctrl *TicketCatalogController) ListOrganizerOrders(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	page, pageSize := parseTicketPage(c)
	filter := service.ParseOrderListFilter(c.Query("status"), c.Query("keyword"), c.Query("q"))
	orders, total, err := ctrl.service.ListOrganizerOrders(
		c.Request.Context(), userID, organizerID, page, pageSize, filter,
	)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.SuccessWithPage(c, orders, total, page, pageSize)
}

func (ctrl *TicketCatalogController) SubmitEventForReview(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	event, err := ctrl.service.SubmitEventForReview(c.Request.Context(), userID, organizerID, eventID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, event)
}

func (ctrl *TicketCatalogController) WithdrawEventReview(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	event, err := ctrl.service.WithdrawEventReview(c.Request.Context(), userID, organizerID, eventID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, event)
}

func (ctrl *TicketCatalogController) UnpublishEvent(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	event, err := ctrl.service.UnpublishEvent(c.Request.Context(), userID, organizerID, eventID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, event)
}

func (ctrl *TicketCatalogController) CancelEvent(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	var input struct {
		Reason     string `json:"reason"`
		RefundPaid *bool  `json:"refund_paid"`
	}
	_ = c.ShouldBindJSON(&input)
	event, err := ctrl.service.CancelEvent(c.Request.Context(), userID, organizerID, eventID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	refundPaid := true
	if input.RefundPaid != nil {
		refundPaid = *input.RefundPaid
	}
	var refund *service.BatchRefundResult
	if refundPaid && ctrl.orders != nil {
		reason := strings.TrimSpace(input.Reason)
		if reason == "" {
			reason = "活动取消批量退款"
		}
		refund, err = ctrl.orders.BatchRefundEvent(
			c.Request.Context(), userID, organizerID, eventID, reason,
		)
		if err != nil {
			writeTicketOrderError(c, err)
			return
		}
	}
	response.Success(c, gin.H{"event": event, "refund": refund})
}

func (ctrl *TicketCatalogController) DisableTicketTier(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	tierID, ok := parseTicketID(c, "tier_id")
	if !ok {
		return
	}
	tier, err := ctrl.service.DisableTicketTier(c.Request.Context(), userID, organizerID, tierID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, tier)
}

func (ctrl *TicketCatalogController) BatchRefundEvent(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&input)
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "主办方批量退款"
	}
	result, err := ctrl.orders.BatchRefundEvent(
		c.Request.Context(), userID, organizerID, eventID, reason,
	)
	if err != nil {
		writeTicketOrderError(c, err)
		return
	}
	response.Success(c, result)
}

func (ctrl *TicketCatalogController) CreateSession(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	var input service.CreateEventSessionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	session, err := ctrl.service.CreateSession(
		c.Request.Context(), userID, organizerID, eventID, input,
	)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, session)
}

func (ctrl *TicketCatalogController) UpdateSession(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	sessionID, ok := parseTicketID(c, "session_id")
	if !ok {
		return
	}
	var input service.CreateEventSessionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	session, err := ctrl.service.UpdateSession(
		c.Request.Context(), userID, organizerID, sessionID, input,
	)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, session)
}

func (ctrl *TicketCatalogController) CreateTicketTier(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	sessionID, ok := parseTicketID(c, "session_id")
	if !ok {
		return
	}
	var input service.CreateTicketTierInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	tier, err := ctrl.service.CreateTicketTier(
		c.Request.Context(), userID, organizerID, sessionID, input,
	)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, tier)
}

func (ctrl *TicketCatalogController) UpdateTicketTier(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	tierID, ok := parseTicketID(c, "tier_id")
	if !ok {
		return
	}
	var input service.UpdateTicketTierInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	tier, err := ctrl.service.UpdateTicketTier(
		c.Request.Context(), userID, organizerID, tierID, input,
	)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, tier)
}

func (ctrl *TicketCatalogController) GetSeatLayout(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	layout, err := ctrl.service.GetSeatLayout(c.Request.Context(), userID, organizerID, eventID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, layout)
}

func (ctrl *TicketCatalogController) SaveSeatLayout(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	organizerID, ok := parseTicketID(c, "organizer_id")
	if !ok {
		return
	}
	eventID, ok := parseTicketID(c, "event_id")
	if !ok {
		return
	}
	var input service.SeatLayoutInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	layout, err := ctrl.service.SaveSeatLayout(c.Request.Context(), userID, organizerID, eventID, input)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, layout)
}

func (ctrl *TicketCatalogController) ListSessionSeats(c *gin.Context) {
	eventID, ok := parseTicketID(c, "id")
	if !ok {
		return
	}
	sessionID, ok := parseTicketID(c, "session_id")
	if !ok {
		return
	}
	seats, err := ctrl.service.ListSessionSeats(c.Request.Context(), eventID, sessionID)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, seats)
}

func parseTicketID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, name+" 格式错误")
		return 0, false
	}
	return id, true
}

func parseOptionalQueryID(c *gin.Context, name string) (int64, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, name+" 格式错误")
		return 0, false
	}
	return id, true
}

func parseTicketPage(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "12"))
	query := service.TicketCatalogListQuery{Page: page, PageSize: pageSize}
	query.Normalize()
	return query.Page, query.PageSize
}

func ticketUserID(c *gin.Context) (int64, bool) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户未授权")
		return 0, false
	}
	return userID, true
}

func writeTicketCatalogError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrTicketResourceNotFound):
		response.Error(c, http.StatusNotFound, response.CodeNotFound, err.Error())
	case errors.Is(err, service.ErrOrganizerForbidden):
		response.Error(c, http.StatusForbidden, response.CodeForbidden, err.Error())
	case errors.Is(err, service.ErrInvalidTicketCatalog),
		errors.Is(err, service.ErrOrganizerUnavailable):
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
	default:
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "票务服务暂时不可用")
	}
}
