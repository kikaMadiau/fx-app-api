package repository

import (
	"testing"

	"fx-app-api/internal/domain/trader/permissions"
)

func TestContainsRole(t *testing.T) {
	if !permissions.ContainsRole([]string{"TRADER", "SUPERVISOR"}, "SUPERVISOR") {
		t.Fatal("combined role was not found")
	}
	if permissions.ContainsRole([]string{"TRADER"}, "SUPERVISOR") {
		t.Fatal("missing role was found")
	}
}
