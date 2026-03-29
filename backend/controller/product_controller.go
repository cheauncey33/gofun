package controller

import (
	"WHU_Snack_GO/service"

	"github.com/gin-gonic/gin"
)

func GetProductHandler(c *gin.Context) {
	var query service.ProductListQuery

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(400, gin.H{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}
	products, total, err := service.GetProductList(query)
	if err != nil {
		c.JSON(500, gin.H{
			"code": 500,
			"msg":  "商品列表获取失败",
		})
		return
	}
	c.JSON(200, gin.H{
		"code":  20,
		"data":  products,
		"total": total,
	})

}
