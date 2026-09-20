package web

import (
	"errors"
	"fmt"
	"testing"

	"github.com/justinlindh/digits/server/internal/household"
)

func TestIsExpectedInviteRedemptionError(t *testing.T) {
	for _, err := range []error{
		household.ErrInviteExpiredOrUsed,
		household.ErrInviteEmailMismatch,
		fmt.Errorf("wrapped: %w", household.ErrInviteExpiredOrUsed),
	} {
		if !isExpectedInviteRedemptionError(err) {
			t.Errorf("isExpectedInviteRedemptionError(%v) = false, want true", err)
		}
	}
	if isExpectedInviteRedemptionError(errors.New("database unavailable")) {
		t.Error("unexpected database error classified as expected")
	}
}
