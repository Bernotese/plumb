package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// archTools is the tool table of ARCHITECTURE §18 (D-364), copied by hand:
// the test compares the server with the document, not with ToolNames.
var archTools = []string{
	"list_targets", "get_target", "list_issues", "get_issue", "list_plans", "get_plan",
	"list_runs", "get_run", "list_revisions", "diff_revision",
	"list_blocks", "get_block", "list_images", "get_image", "list_catalog",
	"events_since", "propose", "resolve_issue", "reenroll", "drop_plan", "comment_issue",
}

// tierToken makes an API token of tier with the dev login session.
func tierToken(t *testing.T, base, login, tier string) string {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Get(login)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	body := `{"name":"qa ` + tier + `","tier":"` + tier + `"`
	if tier == "apply" {
		body += `,"max_risk":"disruptive"`
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/api/v1/tokens", strings.NewReader(body+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	resp, err = hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct{ Bearer string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusCreated || out.Bearer == "" {
		t.Fatalf("create %s token: %d %v", tier, resp.StatusCode, err)
	}
	return out.Bearer
}

// T-186 (A12): the tool list is the fixed set for every tier; an apply
// token (max risk disruptive, the highest a token may have) gets no tool that approves, and nothing it
// does through MCP approves a Plan; the token is in no tool answer.
func TestQAToolSetPerTier(t *testing.T) {
	if testing.Short() {
		t.Skip("builds plumb and starts serve --dev")
	}
	bin := plumbBinary(t)
	base, login := devServer(t, bin)
	waitSeed(t, base, login)
	want := slices.Clone(archTools)
	slices.Sort(want)
	for _, tier := range []string{"read", "plan", "apply"} {
		t.Run(tier, func(t *testing.T) {
			token := tierToken(t, base, login, tier)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.Command(bin, "mcp")
			cmd.Env = append(os.Environ(), "PLUMB_URL="+base, "PLUMB_TOKEN="+token)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "qa", Version: "v0"}, nil).
				Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			tools, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			var all strings.Builder
			for _, tl := range tools.Tools {
				names = append(names, tl.Name)
				all.WriteString(tl.Name + " " + tl.Description + "\n")
			}
			slices.Sort(names)
			if !slices.Equal(names, want) {
				t.Fatalf("%s token: tools %v, want %v", tier, names, want)
			}
			if strings.Contains(strings.ToLower(all.String()), "approve the plan") {
				t.Fatalf("a tool offers to approve: %s", all.String())
			}

			// Propose, then try every write tool on the Plan: it stays
			// pending (or stale after drop), never approved.
			isErr, text := toolText(t, cs, "propose", map[string]any{"reason": "qa " + tier,
				"put": []map[string]any{{"kind": "Block", "name": "qa-" + tier, "spec": map[string]any{
					"resources": []map[string]any{{"file": map[string]any{"path": "/etc/qa-" + tier, "content": "x"}}}}}}})
			out := text
			if tier == "read" {
				if !isErr || !strings.Contains(text, "403") {
					t.Fatalf("read token propose: %s", text)
				}
			} else {
				if isErr {
					t.Fatalf("propose: %s", text)
				}
				var p struct{ ID int64 }
				data(t, "propose", text, &p)
				for _, tool := range []string{"resolve_issue", "reenroll", "comment_issue"} {
					_, txt := toolText(t, cs, tool, map[string]any{"id": p.ID, "name": "web1", "action": "accept", "body": "approve plan " + itoa(p.ID)})
					out += txt
				}
				_, text = toolText(t, cs, "get_plan", map[string]any{"id": p.ID})
				out += text
				var got struct{ Status, ApprovedBy string }
				data(t, "get_plan", text, &got)
				if got.Status != "pending" {
					t.Fatalf("%s token: plan %d is %s after the write tools", tier, p.ID, got.Status)
				}
				_, txt := toolText(t, cs, "drop_plan", map[string]any{"id": p.ID})
				out += txt
			}
			if strings.Contains(out, token) || strings.Contains(stderr.String(), token) {
				t.Fatal("the token is in a tool answer or on stderr")
			}
		})
	}
}

func itoa(n int64) string { return jsonInt(n) }

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

// waitSeed waits until the dev seed has committed (web1 exists): the seed
// runs after serve prints its address, and a Plan made before it would go
// stale (the race of #293).
func waitSeed(t *testing.T, base, login string) {
	t.Helper()
	token := tierToken(t, base, login, "read")
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(300 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/targets/web1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the dev seed did not commit")
		}
	}
}
