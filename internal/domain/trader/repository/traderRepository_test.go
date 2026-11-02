package repository

import (
	"fx-app-api/internal/domain/trader/permissions"
	"testing"
)

func TestBlocksLastAdminRemoval(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		active     bool
		count      int
		nextRole   string
		nextActive bool
		want       bool
	}{
		{"last admin demoted", string(permissions.Admin), true, 1, string(permissions.Trader), true, true},
		{"last admin disabled", string(permissions.Admin), true, 1, string(permissions.Admin), false, true},
		{"one of several admins", string(permissions.Admin), true, 2, string(permissions.Trader), true, false},
		{"non admin removed", string(permissions.Trader), true, 1, "", false, false},
		{"last admin unchanged", string(permissions.Admin), true, 1, string(permissions.Admin), true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := blocksLastAdminRemoval(test.role, test.active, test.count, test.nextRole, test.nextActive)
			if got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}
