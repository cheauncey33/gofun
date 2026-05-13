package validator

import (
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

var Validate *validator.Validate

func Init() {
	Validate = validator.New()
	_ = Validate.RegisterValidation("username", validateUsername)
	_ = Validate.RegisterValidation("password", validatePassword)
	_ = Validate.RegisterValidation("phone", validatePhone)

	// 同时注册到 Gin 的 validator 实例上, 让 binding tag 生效
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		_ = v.RegisterValidation("username", validateUsername)
		_ = v.RegisterValidation("password", validatePassword)
		_ = v.RegisterValidation("phone", validatePhone)
	}
}

func validateUsername(fl validator.FieldLevel) bool {
	s := fl.Field().String()
	return len(s) >= 3 && len(s) <= 20
}

func validatePassword(fl validator.FieldLevel) bool {
	s := fl.Field().String()
	return len(s) >= 6
}

func validatePhone(fl validator.FieldLevel) bool {
	s := fl.Field().String()
	if s == "" {
		return true // 手机号可选
	}
	if len(s) != 11 || s[0] != '1' {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
