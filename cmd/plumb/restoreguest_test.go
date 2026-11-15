package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/driver/proxmox"
)

// T-222: no restorer without Proxmox, a guest backup storage or an install
// id; in restored mode the raw driver is never written.
func TestGuestRestorerOf(t *testing.T) {
	x := proxmox.InstallExtras{GuestBackupStorage: "plumb-backup"}
	if guestRestorerOf(fake.New(), nil, x, "iid", nil) != nil {
		t.Error("a restorer for the fake driver")
	}
	pd := &proxmox.Driver{}
	if guestRestorerOf(pd, nil, proxmox.InstallExtras{}, "iid", nil) != nil || guestRestorerOf(pd, nil, x, "", nil) != nil {
		t.Error("a restorer without storage or install id")
	}
	g := guestRestorerOf(pd, nil, x, "iid", func() bool { return true })
	if g == nil {
		t.Fatal("no restorer")
	}
	if err := g.RestoreGuest(context.Background(), "9001", "plumb-backup:backup/x"); !errors.Is(err, driver.ErrReadOnly) {
		t.Errorf("restored mode: %v", err)
	}
}
