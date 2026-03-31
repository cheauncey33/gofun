package service

import (
	"WHU_Snack_GO/models"
)

type ProductListQuery struct {
	Page     int `form:"page,default=1"`       //第几页
	PageSize int `form:"page_size,default=10"` //每页多少页
}

func GetProductList(query ProductListQuery) ([]models.Product, int64, error) {
	var products []models.Product

}
