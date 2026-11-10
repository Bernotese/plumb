package api

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

func TestTierMatrixCoversContract(t *testing.T) {
	k := loadContract(t)
	paths := k.doc["paths"].(map[string]any)
	for path, item := range paths {
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			op, ok := item.(map[string]any)[method].(map[string]any)
			if !ok || method == "post" && path == "/session" {
				continue // Public login is exercised separately for every tier below.
			}
			var found *endpoint
			for i := range endpoints {
				ep := &endpoints[i]
				concrete, _, _ := strings.Cut(strings.TrimPrefix(ep.path, "/api/v1"), "?")
				want, got := strings.Split(path, "/"), strings.Split(concrete, "/")
				matches := len(want) == len(got)
				if matches {
					for j := range want {
						matches = matches && (want[j] == got[j] || strings.HasPrefix(want[j], "{"))
					}
				}
				if strings.EqualFold(ep.method, method) && matches {
					found = ep
					break
				}
			}
			// A slice 2 operation that is in the contract before its handler
			// (T-027) names the card that adds it. That card adds the matrix
			// row and removes the mark; a mark with a row is stale.
			if card, planned := op["x-plumb-planned"].(string); planned {
				if found != nil {
					t.Errorf("%s %s is in the tier matrix but still has x-plumb-planned: %s", method, path, card)
				}
				continue
			}
			if found == nil {
				t.Errorf("tier matrix omits %s %s", method, path)
				continue
			}
			security, ok := op["security"].([]any)
			if !ok {
				security = k.doc["security"].([]any)
			}
			wantTier, human := TierAdmin, true
			for _, entry := range security {
				if tiers, ok := entry.(map[string]any)["token"].([]any); ok && len(tiers) == 1 {
					wantTier, human = tiers[0].(string), false
				}
			}
			if found.tier != wantTier || found.human != human {
				t.Errorf("%s %s matrix access differs from contract: %+v; want tier=%s human=%v", method, path, found, wantTier, human)
			}
		}
	}
}

func TestLoginForEveryTokenTier(t *testing.T) {
	e := newEnv(t, true)
	for _, c := range []*client{e.anon(), e.token(TierRead, ""), e.token(TierPlan, ""), e.token(TierApply, wire.RiskDisruptive)} {
		rec := c.raw("POST", "/api/v1/session", map[string]string{"username": "admin", "password": e.password})
		if rec.Code != 200 || len(rec.Result().Cookies()) == 0 {
			t.Errorf("public login rejected caller with bearer %t: %d", c.bearer != "", rec.Code)
		}
	}
	e.noSecrets()
}
