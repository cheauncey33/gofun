package service

import (
	"errors"
	"testing"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

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
