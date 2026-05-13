package validator

import (
	"testing"
)

type testRegisterReq struct {
	Username string `validate:"required,username"`
	Password string `validate:"required,password"`
	Phone    string `validate:"phone"`
}

func init() {
	Init()
}

func TestValidateUsername_Valid(t *testing.T) {
	req := testRegisterReq{Username: "john", Password: "123456"}
	err := Validate.Struct(req)
	if err != nil {
		t.Errorf("expected valid username, got error: %v", err)
	}
}

func TestValidateUsername_TooShort(t *testing.T) {
	req := testRegisterReq{Username: "ab", Password: "123456"}
	err := Validate.Struct(req)
	if err == nil {
		t.Error("expected error for too short username")
	}
}

func TestValidateUsername_TooLong(t *testing.T) {
	req := testRegisterReq{Username: "abcdefghijklmnopqrstuvwxyz", Password: "123456"}
	err := Validate.Struct(req)
	if err == nil {
		t.Error("expected error for too long username")
	}
}

func TestValidatePassword_Valid(t *testing.T) {
	req := testRegisterReq{Username: "john", Password: "123456"}
	err := Validate.Struct(req)
	if err != nil {
		t.Errorf("expected valid password, got error: %v", err)
	}
}

func TestValidatePassword_TooShort(t *testing.T) {
	req := testRegisterReq{Username: "john", Password: "12345"}
	err := Validate.Struct(req)
	if err == nil {
		t.Error("expected error for too short password")
	}
}

func TestValidatePhone_Valid(t *testing.T) {
	req := testRegisterReq{Username: "john", Password: "123456", Phone: "13800138000"}
	err := Validate.Struct(req)
	if err != nil {
		t.Errorf("expected valid phone, got error: %v", err)
	}
}

func TestValidatePhone_Empty(t *testing.T) {
	// Phone is optional
	req := testRegisterReq{Username: "john", Password: "123456", Phone: ""}
	err := Validate.Struct(req)
	if err != nil {
		t.Errorf("expected optional phone to pass, got error: %v", err)
	}
}

func TestValidatePhone_Invalid(t *testing.T) {
	req := testRegisterReq{Username: "john", Password: "123456", Phone: "123"}
	err := Validate.Struct(req)
	if err == nil {
		t.Error("expected error for invalid phone")
	}
}

func TestValidatePhone_NotStartingWithOne(t *testing.T) {
	req := testRegisterReq{Username: "john", Password: "123456", Phone: "23800138000"}
	err := Validate.Struct(req)
	if err == nil {
		t.Error("expected error for phone not starting with 1")
	}
}
