package service

import "testing"

func TestValidateCreateOrderInputRejectsInvalidQuantity(t *testing.T) {
	input := CreateOrderInput{
		Items: []CreateOrderItemInput{{ProductID: 1, Num: -1}},
	}

	if err := validateCreateOrderInput(input); err == nil {
		t.Fatal("expected invalid quantity to be rejected")
	}
}

func TestValidateCreateOrderInputRejectsEmptyItems(t *testing.T) {
	if err := validateCreateOrderInput(CreateOrderInput{}); err == nil {
		t.Fatal("expected empty order items to be rejected")
	}
}
