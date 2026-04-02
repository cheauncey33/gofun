package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"
	"fmt"

	"gorm.io/gorm"
)

type CreateOrderInput struct {
	Items []struct {
		ProductID int64 `json:"product_id" binding:"required"`
		Num       int   `json:"num" binding:"required"`
	} `json:"items" binding:"required,dive"`
}

//一种范式
//围绕curd，分析每一动作具体需要修改哪些东西 涉及到哪些crud

// 传入input-用户UserID，用户买的各种商品——【】OrderItem。  每个OrderItem定义为ProductID、Num(购买数量)
// 想一下需要修改哪些东西?要检查哪些表：
// 1、学生表（记录余额、地址）需要动balance (update)
// 2、产品表（记录产品的信息、库存）需要更新（update）
// 3、订单主表和订单细节表需要增加记录（create）
func CreateOrder(user_id int64, input CreateOrderInput) error {
	for _, item := range input.Items {
		redisKey := fmt.Sprintf("snack:stock:%d", item.ProductID)

		newStock, err := common.RDB.DecrBy(common.Ctx, redisKey, int64(item.Num)).Result()
		if err != nil {
			return fmt.Errorf("系统繁忙")
		}
		if newStock < 0 {
			common.RDB.IncrBy(common.Ctx, redisKey, int64(item.Num))
			return fmt.Errorf("商品被抢完了！")
		}
	}
	return common.DB.Transaction(func(tx *gorm.DB) error {
		var totalAmount float64
		var user models.User
		//1、查询学生存到user中并上锁
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&user, user_id).Error; err != nil {
			return fmt.Errorf("用户ID %d 不存在", user_id)
		}
		//2、先检查每种目标商品的库存够不够，同时给商品加锁。够则直接Expr扣除
		for _, item := range input.Items {
			var product models.Product
			fmt.Println(item.ProductID, item.Num)
			//锁住并检查目标商品库存
			if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&product, item.ProductID).Error; err != nil {
				return fmt.Errorf("商品ID %d 不存在", item.ProductID)
			}
			if product.Stock < item.Num {
				return fmt.Errorf("商品%s库存不足 Asked for %d which is %d left", product.Name, item.Num, product.Stock)
			}

			//库存足够则原子扣除
			res := tx.Model(&product).
				Where("id=? AND stock >=?", item.ProductID, item.Num).
				Update("stock", gorm.Expr("stock-?", item.Num))

			if res.Error != nil || res.RowsAffected == 0 {
				return fmt.Errorf("商品 %s 库存扣减失败，可能已经被抢光", product.Name)
			}
			totalAmount += product.Price * float64(item.Num)

		}

		//3、库存够检查user的余额够不够
		if user.Balance < totalAmount {
			return fmt.Errorf("余额不足，总价 %.2f，当前余额%.2f", totalAmount, user.Balance)
		}
		if err := tx.Model(&user).Update("balance", gorm.Expr("balance-?", totalAmount)).Error; err != nil {
			return fmt.Errorf("余额扣减失败")
		}

		//学生、库存的Update操作完成 开始完成Create操作
		//将要插入的记录Order 和OrderItem存为结构体然后Create
		newOrder := models.Order{
			UserID:     user_id,
			TotalPrice: totalAmount,
			Status:     1,
		}
		if err := tx.Create(&newOrder).Error; err != nil {
			return err
		}

		//创建order_item{OrderID,ProductID,Product,Quantity,SnapshotPrice} 所以要遍历输入的每一个item
		for _, item := range input.Items {
			var p models.Product
			tx.First(&p, item.ProductID)

			detail := models.OrderItem{
				OrderID:       newOrder.ID,
				ProductID:     item.ProductID,
				Quantity:      item.Num,
				SnapshotPrice: p.Price,
			}
			if err := tx.Create(&detail).Error; err != nil {
				return err
			}
		}

		// 函数返回 nil，事务自动 Commit；返回 error，事务自动 Rollback
		return nil
	})
}
