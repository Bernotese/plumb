package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/wire"
)

type growRunner struct {
	calls           [][]string
	fs              string
	later, nochange bool
}

func (r *growRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	switch name {
	case "findmnt":
		return []byte("/dev/sda1 " + r.fs), nil
	case "lsblk":
		extra := ""
		if r.later {
			extra = `,{"path":"/dev/sda2","type":"part","pkname":"sda","start":4096,"size":1024}`
		}
		return []byte(`{"blockdevices":[{"path":"/dev/sda","type":"disk","size":10485760},{"path":"/dev/sda1","type":"part","pkname":"sda","start":2048,"size":1048576}` + extra + `]}`), nil
	case "growpart":
		if r.nochange {
			return []byte("NOCHANGE: partition 1"), &resource.ExitError{Code: 1}
		}
	}
	return nil, nil
}
func TestGrow(t *testing.T) {
	for _, tc := range []struct {
		fs              string
		later, nochange bool
		status          wire.Status
	}{
		{"ext4", false, false, wire.StatusApplied}, {"xfs", false, true, wire.StatusUnchanged}, {"ext4", true, false, wire.StatusFailed}, {"btrfs", false, false, wire.StatusFailed},
	} {
		t.Run(tc.fs+string(tc.status), func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "sys/class/block/sda1")
			if err := os.MkdirAll(p, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(p, "partition"), []byte("1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			r := &growRunner{fs: tc.fs, later: tc.later, nochange: tc.nochange}
			a := &Agent{env: resource.NewEnv(dir, r)}
			got := a.grow(context.Background(), wire.GrowBody{MinBytes: 10485760})
			if got.Status != tc.status {
				t.Fatalf("%+v", got)
			}
			if tc.status == wire.StatusFailed {
				for _, c := range r.calls {
					if c[0] == "growpart" {
						t.Fatal("mutated refused root")
					}
				}
				return
			}
			want := []string{"resize2fs", "/dev/sda1"}
			if tc.fs == "xfs" {
				want = []string{"xfs_growfs", "/"}
			}
			if !reflect.DeepEqual(r.calls[len(r.calls)-1], want) {
				t.Fatal(r.calls)
			}
		})
	}
}
