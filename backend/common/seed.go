package common

import (
	"WHU_Snack_GO/models"
	"fmt"
	"math/rand"
)

func SeedData() {
	// 1. 清理旧数据（可选，开发阶段建议开启，防止重复运行脚本导致数据堆积）
	// DB.Exec("SET FOREIGN_KEY_CHECKS = 0;")
	// DB.Where("1 = 1").Delete(&models.OrderItem{})
	// DB.Where("1 = 1").Delete(&models.Order{})
	// DB.Where("1 = 1").Delete(&models.Product{})
	// DB.Where("1 = 1").Delete(&models.User{})
	// DB.Where("1 = 1").Delete(&models.Dormitory{})
	// DB.Exec("SET FOREIGN_KEY_CHECKS = 1;")

	// 2. 生成宿舍数据 (Seed Dormitories)
	dorms := []models.Dormitory{
		{BuildingName: "湖滨13舍", RoomNumber: "404"},
		{BuildingName: "湖滨13舍", RoomNumber: "502"},
		{BuildingName: "枫园14舍", RoomNumber: "101"},
		{BuildingName: "信息学部1舍", RoomNumber: "233"},
	}
	for i := range dorms {
		DB.FirstOrCreate(&dorms[i], models.Dormitory{BuildingName: dorms[i].BuildingName, RoomNumber: dorms[i].RoomNumber})
	}

	// 3. 生成测试用户 (Seed Users)
	users := []models.User{
		{Username: "test_user_1", Password: "hashed_password", Balance: 500.00, DormID: dorms[0].ID},
		{Username: "test_user_2", Password: "hashed_password", Balance: 50.00, DormID: dorms[1].ID},
	}
	for i := range users {
		DB.FirstOrCreate(&users[i], models.User{Username: users[i].Username})
	}

	// 4. 批量生成 50 条零食数据 (Seed 50 Products)
	names := []string{"可口可乐", "乐事薯片", "卫龙辣条", "三只松鼠坚果", "奥利奥", "康师傅红烧牛肉面", "盲盒", "元气森林", "维他柠檬茶", "德芙巧克力"}
	flavors := []string{"原味", "烧烤味", "香辣味", "青柠味", "草莓味", "限定款"}

	for i := 1; i <= 50; i++ {
		name := fmt.Sprintf("%s(%s)%d", names[rand.Intn(len(names))], flavors[rand.Intn(len(flavors))], i)
		price := float64(rand.Intn(1500)+200) / 100.0 // 2.00 到 17.00 之间
		stock := rand.Intn(100) + 10

		product := models.Product{
			Name:  name,
			Price: price,
			Stock: stock,
		}
		DB.Create(&product)
	}

	fmt.Println("✅ 种子数据初始化完成！已生成 4 个宿舍、2 个测试用户和 50 件零食。")
}
