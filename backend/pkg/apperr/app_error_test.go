package apperr

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	err := New(40401, "用户不存在")
	if err.Code != 40401 {
		t.Errorf("expected code 40401, got %d", err.Code)
	}
	if err.Message != "用户不存在" {
		t.Errorf("expected message '用户不存在', got '%s'", err.Message)
	}
	if err.Err != nil {
		t.Error("expected nil underlying error")
	}
}

func TestWrap(t *testing.T) {
	underlying := errors.New("db connection refused")
	err := Wrap(50001, "数据库错误", underlying)

	if err.Code != 50001 {
		t.Errorf("expected code 50001, got %d", err.Code)
	}
	if !errors.Is(err, underlying) {
		t.Error("expected Unwrap to return underlying error")
	}
}

func TestError_String(t *testing.T) {
	err := New(60001, "库存不足")
	s := err.Error()
	expected := "[60001] 库存不足"
	if s != expected {
		t.Errorf("expected '%s', got '%s'", expected, s)
	}
}

func TestError_StringWithWrap(t *testing.T) {
	underlying := errors.New("timeout")
	err := Wrap(50000, "服务器错误", underlying)
	s := err.Error()
	if s == "" {
		t.Error("expected non-empty error string")
	}
}
