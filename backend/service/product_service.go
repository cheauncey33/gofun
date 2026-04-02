package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"
	"fmt"
)

type ProductListQuery struct {
	Page     int `form:"page,default=1"`
	PageSize int `form:"page_size,default=10"`
}

// service 完成什么？对数据库的crud
// GetPRoductList完成什么？按照传来的ProductListQuery查询目标页的商品记录，存储到返回值中
// 所以要read product表
func GetProductList(query ProductListQuery) ([]models.Product, int64, error) {
	var products []models.Product
	var total int64

	//先统计一共有多少商品 便于展示一共多少页
	common.DB.Model(&models.Product{}).Count(&total)

	offset := (query.Page - 1) * query.PageSize
	err := common.DB.Limit(query.PageSize).Offset(offset).Find(&products).Error
	return products, total, err

}
func InitProductStockToRedis() error {
	var products []models.Product
	if err := common.DB.Find(&products).Error; err != nil {
		return fmt.Errorf("读取商品数据失败: %v", err)
	}
	for _, p := range products {
		redisKey := fmt.Sprintf("snack:stock:%d", p.ID)
		err := common.RDB.Set(common.Ctx, redisKey, p.Stock, 0).Err()
		if err != nil {
			return fmt.Errorf("预热商品%v-%v失败", p.Name, p.ID, err)
		}
	}
	fmt.Println("成功预热", len(products), "个商品到redis中！")
	return nil
}
