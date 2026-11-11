package main

import (
	"errors"
	"testing"
)

func TestDiskLimit(t *testing.T) {
	for v, want := range map[string]int{"": 85, "50": 50, "95": 95, " 80 ": 80} {
		got, err := diskLimit(func(string) string { return v })
		if err != nil || got != want {
			t.Errorf("%q: %d %v", v, got, err)
		}
	}
	for _, v := range []string{"49", "96", "0", "100", "85%", "8.5", "abc", "-85"} {
		_, err := diskLimit(func(string) string { return v })
		var ce *errConfig
		if !errors.As(err, &ce) {
			t.Errorf("%q: %v", v, err)
		}
	}
}
