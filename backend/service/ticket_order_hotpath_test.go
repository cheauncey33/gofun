package service

import (
	"errors"
	"testing"

	"gofun/models"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

func TestParseIdempotencyCache(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		wantID     int64
		wantStatus models.TicketOrderStatus
		wantOK     bool
	}{
		{name: "valid with status", raw: "123|pending_payment", wantID: 123, wantStatus: models.TicketOrderStatusPendingPayment, wantOK: true},
		{name: "empty status defaults queued", raw: "123|", wantID: 123, wantStatus: models.TicketOrderStatusQueued, wantOK: true},
		{name: "non numeric id", raw: "abc|queued", wantOK: false},
		{name: "missing status separator", raw: "123", wantOK: false},
		{name: "non positive id", raw: "-1|queued", wantOK: false},
		{name: "zero id", raw: "0|queued", wantOK: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id, status, ok := parseIdempotencyCache(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("parseIdempotencyCache(%q) ok = %v, want %v", tt.raw, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if id != tt.wantID || status != tt.wantStatus {
				t.Fatalf("parseIdempotencyCache(%q) = (%d, %s), want (%d, %s)", tt.raw, id, status, tt.wantID, tt.wantStatus)
			}
		})
	}
}

func TestIsDuplicateStorageKeyError(t *testing.T) {
	t.Parallel()
	if !isDuplicateStorageKeyError(&mysqlDriver.MySQLError{Number: 1062, Message: "duplicate"}) {
		t.Fatal("MySQL 1062 should be classified as duplicate key")
	}
	if isDuplicateStorageKeyError(&mysqlDriver.MySQLError{Number: 1213, Message: "deadlock"}) {
		t.Fatal("deadlock should not be classified as duplicate key")
	}
}

func TestIsRetryableMySQLTransactionError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "deadlock",
			err:  &mysqlDriver.MySQLError{Number: 1213, Message: "deadlock"},
			want: true,
		},
		{
			name: "lock wait timeout",
			err:  &mysqlDriver.MySQLError{Number: 1205, Message: "lock wait timeout"},
			want: true,
		},
		{
			name: "duplicate key",
			err:  &mysqlDriver.MySQLError{Number: 1062, Message: "duplicate"},
			want: false,
		},
		{
			name: "wrapped deadlock",
			err: errors.Join(
				errors.New("transaction failed"),
				&mysqlDriver.MySQLError{Number: 1213, Message: "deadlock"},
			),
			want: true,
		},
		{
			name: "non mysql error",
			err:  errors.New("network error"),
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetryableMySQLTransactionError(tt.err); got != tt.want {
				t.Fatalf("isRetryableMySQLTransactionError() = %v, want %v", got, tt.want)
			}
		})
	}
}
