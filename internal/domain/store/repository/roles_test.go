package repository

import "testing"

func TestHasRole(t *testing.T) {
	if !hasRole([]string{"TRADER", "SUPERVISOR"}, "SUPERVISOR") {
		t.Fatal("combined role was not found")
	}
	if hasRole([]string{"TRADER"}, "SUPERVISOR") {
		t.Fatal("missing role was found")
	}
}
