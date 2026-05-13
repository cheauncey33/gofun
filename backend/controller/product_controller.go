package controller

import (
	"WHU_Snack_GO/pkg/response"
	"WHU_Snack_GO/service"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func GetProductHandler(c *gin.Context) {
	var query service.ProductListQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	query.Normalize()
	products, total, err := service.GetProductList(query)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "商品列表获取失败")
		return
	}
	response.SuccessWithPage(c, products, total, query.Page, query.PageSize)
}

func GetProductDetailHandler(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "商品ID格式错误")
		return
	}
	product, err := service.GetProductDetail(id)
	if err != nil {
		response.Error(c, http.StatusNotFound, response.CodeProductNotFound, "商品不存在")
		return
	}
	response.Success(c, product)
}

func GetCategoryListHandler(c *gin.Context) {
	categories, err := service.GetCategoryList()
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "分类列表获取失败")
		return
	}
	response.Success(c, categories)
}

func GetCategoryProductsHandler(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "分类ID格式错误")
		return
	}

	var query service.ProductListQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	query.Normalize()
	query.CategoryID = &id

	products, total, err := service.GetProductList(query)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "商品列表获取失败")
		return
	}
	response.SuccessWithPage(c, products, total, query.Page, query.PageSize)
}
