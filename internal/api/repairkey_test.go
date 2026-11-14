package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/repair"
	"github.com/Bernotese/plumb/internal/resource"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-191: the repair key, its rotation and the repair status in the API.

type keyEnv struct {
	*env
	svc  *repair.Service
	logs *lockedBuf
}

func newKeyEnv(t *testing.T) *keyEnv {
	k := &keyEnv{logs: &lockedBuf{}}
	envLog = slog.New(slog.NewTextHandler(k.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	defer func() { envLog = quiet }()
	k.env = newEnv(t, false, func(c *plan.Config) {
		svc, err := repair.New(repair.Config{Store: c.Store, Secrets: c.Secrets, Events: c.Events, Logger: c.Logger, From: []string{"192.0.2.10"},
			Rewatch: func(ctx context.Context) { k.eng.RewatchAll(ctx) }})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.EnsureKey(t.Context()); err != nil {
			t.Fatal(err)
		}
		k.svc = svc
		c.Repair.Transport, c.Repair.Watch, c.Repair.OnReport = svc, svc.Watch, svc.OnReport
	})
	k.srv.cfg.RepairKeys = k.svc
	return k
}

// privateNeedles are the private key bytes in the forms a leak could take.
func (k *keyEnv) privateNeedles() [][]byte {
	k.t.Helper()
	rows, err := k.st.RepairKeys(k.ctx)
	if err != nil {
		k.t.Fatal(err)
	}
	var out [][]byte
	for _, r := range rows {
		seed, err := k.sec.OpenBlob(secret.PurposeRepairKey, "repair-key/"+r.ID, r.Nonce, r.Sealed)
		if err != nil {
			k.t.Fatal(err)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		out = append(out, seed, []byte(base64.StdEncoding.EncodeToString(seed)), []byte(base64.RawStdEncoding.EncodeToString(seed)),
			[]byte(base64.StdEncoding.EncodeToString(priv)), []byte(base64.StdEncoding.EncodeToString(priv[:48])))
	}
	return out
}

// TestRepairKeyRotateAPI: admin session only; settings show the
// fingerprints; rotation and revoke with their 409s; the event names only
// fingerprints.
func TestRepairKeyRotateAPI(t *testing.T) {
	k := newKeyEnv(t)
	adm := k.admin()
	for _, c := range []*client{k.token(TierApply, wire.RiskDisruptive), k.token(TierPlan, "")} {
		if code, body := c.do("POST", "/api/v1/settings/repair-key/rotate", nil); code != 403 {
			t.Fatalf("token rotate: %d %s", code, body)
		}
	}
	code, body := adm.do("GET", "/api/v1/settings", nil)
	var s struct {
		RepairKey *struct {
			Fingerprint string
			Previous    *struct{ Targets []string }
		} `json:"repair_key"`
	}
	json.Unmarshal(body, &s)
	if code != 200 || s.RepairKey == nil || !strings.HasPrefix(s.RepairKey.Fingerprint, "SHA256:") || s.RepairKey.Previous != nil {
		t.Fatalf("settings: %d %s", code, body)
	}
	first := s.RepairKey.Fingerprint
	if code, body := adm.do("POST", "/api/v1/settings/repair-key/rotate", map[string]any{"revoke_old": true}); code != 409 || !strings.Contains(string(body), "nothing-to-revoke") {
		t.Fatalf("revoke without previous: %d %s", code, body)
	}
	code, body = adm.do("POST", "/api/v1/settings/repair-key/rotate", nil)
	if code != 200 || !strings.Contains(string(body), first) || !strings.Contains(string(body), `"previous":{`) {
		t.Fatalf("rotate: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/settings/repair-key/rotate", map[string]any{}); code != 409 || !strings.Contains(string(body), "rotation-running") {
		t.Fatalf("second rotate: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/settings/repair-key/rotate", map[string]any{"revoke_old": true}); code != 200 || strings.Contains(string(body), first) {
		t.Fatalf("revoke: %d %s", code, body)
	}
	if code, _ := adm.do("POST", "/api/v1/settings/repair-key/rotate", map[string]any{"bad": 1}); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}
}

// TestRepairStatusAndWarning: the agent's repair report reaches
// TargetStatus.repair; a firewall whose ssh_sources leave out the
// controller makes the status off (firewall) and the Plan warns.
func TestRepairStatusAndWarning(t *testing.T) {
	k := newKeyEnv(t)
	adm := k.admin()
	k.seedAgent(adm, wire.Capabilities{Commands: wire.AgentCommands(), Resources: resource.Capabilities()})
	status := func() *plan.RepairStatus {
		code, body := adm.do("GET", "/api/v1/targets/web1", nil)
		var v struct {
			Status struct{ Repair *plan.RepairStatus }
		}
		if code != 200 || json.Unmarshal(body, &v) != nil {
			t.Fatalf("target: %d %s", code, body)
		}
		return v.Status.Repair
	}
	if st := status(); st == nil || st.State != plan.RepairOff || !slices.Contains(st.Reasons, "no-line") {
		t.Fatalf("before a report: %+v", st)
	}
	w := k.svc.Watch(k.ctx)
	fp := "SHA256:" + strings.Repeat("A", 43)
	rep := wire.RepairReport{Lines: []string{w.Keys[0].ID}, SSHD: wire.RepairSSHD{Status: wire.SSHDOk, Port: 22}, HostKeys: []string{fp}}
	if code := k.agents["web1"].Report(k.ctx, wire.Report{Type: wire.ReportRepair, Repair: &rep}); code != 204 {
		t.Fatalf("repair report: %d", code)
	}
	if st := status(); st == nil || st.State != plan.RepairReady || !st.KeyConfirmed || len(st.HostKeys) != 1 {
		t.Fatalf("after the report: %+v", st)
	}
	// A bad report is refused by the link (400).
	bad := wire.RepairReport{SSHD: wire.RepairSSHD{Status: "x"}}
	if code := k.agents["web1"].Report(k.ctx, wire.Report{Type: wire.ReportRepair, Repair: &bad}); code != 400 {
		t.Fatalf("bad repair report: %d", code)
	}
	// A firewall that lets only 198.51.100.0/24 reach SSH.
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "fw",
		"put": fwTarget("web1", `"ssh_sources":"198.51.100.0/24"`)})
	if code != 201 || !strings.Contains(string(body), "the repair fallback is off") {
		t.Fatalf("firewall plan: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	approve(t, adm, p.ID)
	k.eng.Wait()
	if st := status(); st.State != plan.RepairOff || !slices.Contains(st.Reasons, "firewall") {
		t.Fatalf("after the firewall: %+v", st)
	}
	// The controller address in ssh_sources: no warning.
	code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "fw2",
		"put": fwTarget("web1", `"ssh_sources":"192.0.2.0/24"`)})
	if code != 201 || strings.Contains(string(body), "repair fallback") {
		t.Fatalf("good firewall plan: %d %s", code, body)
	}
}

// TestRepairKeyLeakScan (T-140 §3): the private key is in no export (with
// and without passphrase), no GET /secrets or /settings answer, no event,
// no log line and no backup file (only inside the encrypted DB).
func TestRepairKeyLeakScan(t *testing.T) {
	k := newKeyEnv(t)
	adm := k.admin()
	k.seedAgent(adm, slice3Caps())
	adm.do("POST", "/api/v1/settings/repair-key/rotate", nil)
	needles := k.privateNeedles()
	var hay [][]byte
	for _, pass := range []string{"", "a-long-export-passphrase"} {
		code, _, body := k.export(adm, pass)
		if code != 200 {
			t.Fatalf("export %q: %d", pass, code)
		}
		hay = append(hay, []byte(body))
	}
	for _, path := range []string{"/api/v1/secrets", "/api/v1/settings", "/api/v1/targets/web1"} {
		_, body := adm.do("GET", path, nil)
		hay = append(hay, body)
	}
	if code, body := adm.do("POST", "/api/v1/backups", nil); code != 202 {
		t.Fatalf("backup: %d %s", code, body)
	}
	k.backups.Wait()
	files, _ := filepath.Glob(filepath.Join(k.dir, "*", "*.pbk"))
	more, _ := filepath.Glob(filepath.Join(k.dir, "*.pbk"))
	files = append(files, more...)
	if len(files) == 0 {
		filepath.WalkDir(k.dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && strings.HasSuffix(p, ".pbk") {
				files = append(files, p)
			}
			return nil
		})
	}
	if len(files) == 0 {
		t.Fatal("no backup file")
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		hay = append(hay, b)
	}
	evs, _ := k.st.EventsAfter(k.ctx, 0, 100000)
	for _, ev := range evs {
		hay = append(hay, ev.Data)
	}
	k.mu.Lock()
	hay = append(hay, k.seen...)
	k.mu.Unlock()
	hay = append(hay, []byte(k.logs.String()))
	for i, h := range hay {
		for _, n := range needles {
			if bytes.Contains(h, n) {
				t.Fatalf("haystack %d holds private key bytes", i)
			}
		}
	}
	// The public key is in the watch list only: no secret, but no leak
	// test can pass by an empty haystack.
	if !strings.Contains(string(hay[0]), "web1") {
		t.Fatal("the export is empty")
	}
}

// lastRepairWatch returns the repair key ids of the last watch command
// that the agent of web1 took, and how many watches it took.
func (k *keyEnv) lastRepairWatch() ([]string, int) {
	k.t.Helper()
	var ids []string
	n := 0
	a := k.agents["web1"]
	bodies, types := a.Bodies(), a.Commands()
	for i, b := range bodies {
		if i >= len(types) || types[i] != wire.CommandWatch {
			continue
		}
		var w wire.WatchBody
		if json.Unmarshal(b, &w) != nil {
			continue
		}
		n++
		ids = nil
		if w.Repair != nil {
			for _, key := range w.Repair.Keys {
				ids = append(ids, key.ID)
			}
		}
	}
	return ids, n
}

// TestRepairRotatePushesWatch (review S2 of #298): a rotation and a
// revoke reach every connected agent at once, with the new key set.
func TestRepairRotatePushesWatch(t *testing.T) {
	k := newKeyEnv(t)
	adm := k.admin()
	k.seedAgent(adm, wire.Capabilities{Commands: wire.AgentCommands(), Resources: resource.Capabilities()})
	k.waitWatch("web1")
	wait := func(want int) []string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if ids, n := k.lastRepairWatch(); n >= want {
				return ids
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("no watch number %d", want)
		return nil
	}
	ids, n := k.lastRepairWatch()
	if len(ids) != 1 {
		t.Fatalf("first watch keys %v", ids)
	}
	old := ids[0]
	if code, body := adm.do("POST", "/api/v1/settings/repair-key/rotate", nil); code != 200 {
		t.Fatalf("rotate: %d %s", code, body)
	}
	if ids := wait(n + 1); len(ids) != 2 || ids[1] != old {
		t.Fatalf("after rotate %v", ids)
	}
	if code, body := adm.do("POST", "/api/v1/settings/repair-key/rotate", map[string]any{"revoke_old": true}); code != 200 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	if ids := wait(n + 2); len(ids) != 1 || ids[0] == old {
		t.Fatalf("after revoke %v", ids)
	}
}
