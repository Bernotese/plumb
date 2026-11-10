package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/api"
	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/link/linktest"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// The agent binary is built first by make build into agentbin/ (D-071).
// On a clean checkout the directory holds only .gitkeep and the binary
// serves no agent.
//
//go:embed all:agentbin
var agentFiles embed.FS

func agentBinary() []byte {
	b, err := agentFiles.ReadFile("agentbin/plumb-agent")
	if err != nil {
		return nil
	}
	return b
}

// devTargets are the simulated guests of --dev.
var devTargets = []string{"web1", "web2"}

func devDocs() []state.Doc {
	d := func(kind, name, spec string) state.Doc {
		return state.Doc{Kind: kind, Name: name, Spec: json.RawMessage(spec)}
	}
	docs := []state.Doc{
		d(state.KindImage, "ubuntu-26.04", `{"title":"Ubuntu 26.04 (simulated)","type":"vm",`+
			`"url":"https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64.img",`+
			`"sha256":"0000000000000000000000000000000000000000000000000000000000000000"}`),
		d(state.KindBlock, "motd", `{"title":"Message of the day","params":{"type":"object","properties":{`+
			`"text":{"type":"string","title":"Text","default":"Managed by Plumb."}}},`+
			`"resources":[{"file":{"path":"/etc/motd","content":"{{ .text }}\n","mode":"0644","owner":"root","group":"root"}}]}`),
		d(state.KindBlock, "nginx", `{"title":"Nginx","params":{"type":"object","properties":{`+
			`"server_name":{"type":"string","title":"Server name","default":"_"}}},"resources":[`+
			`{"package":{"name":"nginx"}},`+
			`{"file":{"path":"/etc/nginx/sites-enabled/default","content":"server { listen 80; server_name {{ .server_name }}; }\n"}},`+
			`{"service":{"name":"nginx","restart_on":["file:/etc/nginx/sites-enabled/default"]}}]}`),
	}
	for _, t := range devTargets {
		docs = append(docs, d(state.KindTarget, t, `{"type":"vm","image":"ubuntu-26.04",`+
			`"guest":{"cores":2,"memory":2048,"disk":20,"bridge":"vmbr0"},`+
			`"blocks":[{"use":"motd","params":{"text":"Hello from `+t+`"}},{"use":"nginx","params":{"server_name":"`+t+`.lan"}}]}`))
	}
	return docs
}

// seedDev starts a simulated agent per dev target and, on an empty
// store, plans and approves the seed documents (D-039). With demo it adds
// the demo data of D-080: one open drift Issue and one
// pending Plan.
func seedDev(ctx context.Context, st *store.Store, eng *plan.Engine, lk *link.Link, drv *fake.Driver, demo bool, log *slog.Logger) error {
	agents := map[string]*linktest.Agent{}
	for _, t := range devTargets {
		token := func() (string, bool) {
			b, err := st.GetBinding(ctx, state.KindTarget, t)
			if err != nil {
				return "", false
			}
			boot, ok := drv.Bootstrap(driver.GuestID(b.Ref))
			return boot.Token, ok
		}
		a := linktest.New(lk.Handler(), token)
		agents[t] = a
		go a.Run(ctx)
	}
	if _, err := st.Head(ctx); err == nil {
		if demo {
			log.Info("demo data skipped: store already seeded")
		}
		return nil // seeded already
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	p, err := eng.Propose(ctx, plan.Input{Proposal: plan.Proposal{Put: devDocs()}, Reason: "dev seed", Author: "system"})
	if err != nil {
		return err
	}
	if _, err := eng.Approve(ctx, p.ID, "system", nil); err != nil {
		return err
	}
	log.Info("dev seed approved", "plan", p.ID, "targets", devTargets)
	if !demo {
		return nil
	}
	return seedDemo(ctx, st, eng, lk, agents, log)
}

// seedDemo waits for the seed Run and adds the demo data of D-080.
func seedDemo(ctx context.Context, st *store.Store, eng *plan.Engine, lk *link.Link, agents map[string]*linktest.Agent, log *slog.Logger) error {
	eng.Wait()
	deadline := time.Now().Add(30 * time.Second)
	for _, t := range devTargets {
		for !lk.Online(t) {
			if time.Now().After(deadline) {
				return fmt.Errorf("dev demo: %s is not online", t)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	// An open drift Issue: someone edits the motd of web2.
	if err := agents["web2"].Change(ctx, "file:/etc/motd", map[string]any{
		"path": "/etc/motd", "content": "Edited by hand\n", "mode": "0644", "owner": "root", "group": "root"}); err != nil {
		return err
	}
	// A pending Plan: a new motd text for web1.
	docs := devDocs()
	var put []state.Doc
	for _, d := range docs {
		if d.Kind == state.KindTarget && d.Name == "web1" {
			d.Spec = json.RawMessage(strings.Replace(string(d.Spec), "Hello from web1", "Hello from web1, now with a plan", 1))
			put = append(put, d)
		}
	}
	p, err := eng.Propose(ctx, plan.Input{Proposal: plan.Proposal{Put: put}, Reason: "demo: new motd for web1", Author: "user:" + api.DevUser})
	if err != nil {
		return err
	}
	log.Info("dev demo seeded", "pending_plan", p.ID)
	return nil
}
