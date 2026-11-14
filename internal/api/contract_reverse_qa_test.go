package api

import (
	"regexp"
	"strings"
	"testing"
)

// #251: check fixture -> contract as well as contract -> fixture.
func TestFixturesBelongToContractQA(t *testing.T) {
	paths := loadContract(t).doc["paths"].(map[string]any)
	for _, ep := range endpoints {
		concrete, _, _ := strings.Cut(strings.TrimPrefix(ep.path, "/api/v1"), "?")
		matches := 0
		for path, item := range paths {
			if _, ok := item.(map[string]any)[strings.ToLower(ep.method)]; !ok {
				continue
			}
			pattern := regexp.MustCompile("^" + regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, `[^/]+`) + "$")
			if pattern.MatchString(concrete) {
				matches++
			}
		}
		if matches != 1 {
			t.Errorf("fixture %s %s matches %d contract operations, want 1", ep.method, ep.path, matches)
		}
	}
	e := newEnv(t, true)
	for _, c := range []*client{e.anon(), e.admin()} {
		if code, body := c.do("GET", "/api/v1/qa-not-in-contract", nil); code != 404 {
			t.Errorf("unknown route: %d %s", code, body)
		}
	}
}
