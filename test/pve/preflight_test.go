//go:build pve

package pve_test

import "testing"

// Explicitly safe to run alone: no SSH, login, Plan, or guest mutation.
func TestRealControllerReadOnly(t *testing.T) {
	a := settings(t)
	s := a.call("GET", "/session", nil, 200)
	if s["tier"] != "apply" || s["max_risk"] != "disruptive" {
		t.Fatal("slice requires an apply token capped at disruptive risk")
	}
	for _, kind := range []string{"targets", "images", "blocks"} {
		a.documents(kind)
	}
	t.Log("token authentication and document lists passed; no objects created")
}
