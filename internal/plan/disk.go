package plan

// The disk gate (T-074, D-203, D-219): one check and one reservation for
// every guest create and every image build, so that creates of one Run and
// builds that run at the same time count together. A full thin pool gives
// I/O errors in every guest on it (host B4).

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
)

// DefaultDiskLimitPercent is the limit of D-219 (the user's answer 7, D-218).
const DefaultDiskLimitPercent = 85

// GiB is 2^30 bytes: guest and build disks are whole GiB.
const GiB = int64(1) << 30

// diskReadTimeout bounds the DiskSpace read: the gate holds its mutex
// while it reads, so a hung read must not block every create (review N1).
var diskReadTimeout = 30 * time.Second

// DiskBytes is gib GiB in bytes. A value whose bytes do not fit in an
// int64 gives math.MaxInt64, which the gate always refuses (review S1:
// an overflow must never wrap to a small or negative size).
func DiskBytes(gib int) int64 {
	if gib < 0 || int64(gib) > math.MaxInt64/GiB {
		return math.MaxInt64
	}
	return int64(gib) * GiB
}

// addBytes adds two sizes and stops at math.MaxInt64 (review S1).
func addBytes(a, b int64) int64 {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// ErrDiskFull marks a refusal of the disk gate. Its text names the
// storage, the numbers and the limit; Plans and Run steps show it as is.
var ErrDiskFull = errors.New("the disk storage would be too full")

// DiskGate checks and reserves space on the disk storage of guests. Check
// and reservation run under one mutex. A reservation ends with its release:
// after the create or the build, the real use that the driver reads counts.
type DiskGate struct {
	drv   driver.Driver
	limit int

	mu       sync.Mutex
	reserved int64
}

// NewDiskGate returns the gate with a limit in percent (DefaultDiskLimitPercent when 0).
func NewDiskGate(drv driver.Driver, limitPercent int) *DiskGate {
	if limitPercent == 0 {
		limitPercent = DefaultDiskLimitPercent
	}
	return &DiskGate{drv: drv, limit: limitPercent}
}

// Limit is the limit in percent.
func (g *DiskGate) Limit() int { return g.limit }

// Reserved is the sum of the reservations that run now
// (DiskUse.reserved_bytes of the API).
func (g *DiskGate) Reserved() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reserved
}

// Reserve checks that bytes more fit under the limit and reserves them.
// what names the use in the refusal text, for example "the 20 GiB disk of
// web1". The caller must call release exactly once when its create or
// build ended (also when it failed). A refusal wraps ErrDiskFull; a
// storage that cannot be read is another error: both mean "do not start".
func (g *DiskGate) Reserve(ctx context.Context, bytes int64, what string) (release func(), err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.check(ctx, bytes, what); err != nil {
		return nil, err
	}
	g.reserved += bytes
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.reserved -= bytes
		})
	}, nil
}

// Check is Reserve without a reservation, for the warning of a Plan.
func (g *DiskGate) Check(ctx context.Context, bytes int64, what string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.check(ctx, bytes, what)
}

func (g *DiskGate) check(ctx context.Context, bytes int64, what string) error {
	rctx, cancel := context.WithTimeout(ctx, diskReadTimeout)
	defer cancel()
	ds, err := g.drv.DiskSpace(rctx)
	if err != nil {
		return fmt.Errorf("cannot read the use of the disk storage, so %s is not allowed: %w", what, err)
	}
	// The sum is exact in big.Int: no input can wrap it below the limit
	// (review S1). A negative size is never room.
	if bytes < 0 || ds.UsedBytes < 0 {
		return fmt.Errorf("%w: %s: bad size for %s", ErrDiskFull, ds.Storage, what)
	}
	need := new(big.Int).Add(big.NewInt(ds.UsedBytes), big.NewInt(g.reserved))
	need.Add(need, big.NewInt(bytes))
	if overLimitBig(need, ds.TotalBytes, g.limit) {
		return fmt.Errorf("%w: %s would be %s %% full with %s: %s used, %s held by running creates and builds, %s in total, limit %d %%",
			ErrDiskFull, ds.Storage, percentUpBig(need, ds.TotalBytes), what, gibText(ds.UsedBytes), gibText(g.reserved), gibText(ds.TotalBytes), g.limit)
	}
	// Unknown metadata use is no refusal (T-062 card); known and over is.
	if ds.MetaKnown && driver.OverLimit(ds.MetaUsedBytes, ds.MetaTotalBytes, g.limit) {
		return fmt.Errorf("%w: the thin-pool metadata of %s is %s %% full (%s of %s), limit %d %%, so %s is not allowed",
			ErrDiskFull, ds.Storage, percentUp(ds.MetaUsedBytes, ds.MetaTotalBytes), mibText(ds.MetaUsedBytes), mibText(ds.MetaTotalBytes), g.limit, what)
	}
	return nil
}

// overLimitBig is driver.OverLimit for a need that may not fit in an int64.
func overLimitBig(need *big.Int, total int64, limitPercent int) bool {
	if total <= 0 {
		return true
	}
	n := new(big.Int).Mul(need, big.NewInt(100))
	l := new(big.Int).Mul(big.NewInt(int64(limitPercent)), big.NewInt(total))
	return n.Cmp(l) > 0
}

// percentUpBig is percentUp for a big need.
func percentUpBig(need *big.Int, total int64) string {
	if total <= 0 {
		return "100+"
	}
	f, _ := new(big.Float).SetInt(need).Float64()
	return fmt.Sprintf("%.1f", math.Ceil(f*1000/float64(total))/10)
}

// percentUp shows a share with one decimal, rounded up: a refused value
// never shows the limit itself (85.01 % → "85.1").
func percentUp(part, total int64) string {
	if total <= 0 {
		return "100+"
	}
	return fmt.Sprintf("%.1f", math.Ceil(float64(part)*1000/float64(total))/10)
}

func gibText(b int64) string { return fmt.Sprintf("%.1f GiB", float64(b)/float64(GiB)) }
func mibText(b int64) string { return fmt.Sprintf("%.0f MiB", float64(b)/float64(1<<20)) }
