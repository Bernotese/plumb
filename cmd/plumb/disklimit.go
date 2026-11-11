package main

import (
	"strconv"
	"strings"

	"github.com/Bernotese/plumb/internal/plan"
)

// diskLimitVar sets the limit of the disk gate for guest creates and image
// builds (D-219). Default 85 (the user's answer 7, D-218).
const diskLimitVar = "PLUMB_DISK_LIMIT_PERCENT"

// diskLimit reads PLUMB_DISK_LIMIT_PERCENT: empty is the default, else a
// whole number from 50 to 95.
func diskLimit(getenv func(string) string) (int, error) {
	v := strings.TrimSpace(getenv(diskLimitVar))
	if v == "" {
		return plan.DefaultDiskLimitPercent, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 50 || n > 95 {
		return 0, configErr("%s is %q; it must be a whole number from 50 to 95 (default %d)", diskLimitVar, v, plan.DefaultDiskLimitPercent)
	}
	return n, nil
}
