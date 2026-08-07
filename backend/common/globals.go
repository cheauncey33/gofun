package common

import "gorm.io/gorm"

// DB 由 container.NewContainer 注入，供 Auth/Admin 等中间件兼容层使用。
var DB *gorm.DB
