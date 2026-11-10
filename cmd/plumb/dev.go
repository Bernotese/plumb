package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"log/slog"

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
// store, plans and approves the seed documents (D-039).
func seedDev(ctx context.Context, st *store.Store, eng *plan.Engine, lk *link.Link, drv *fake.Driver, log *slog.Logger) error {
	for _, t := range devTargets {
		token := func() (string, bool) {
			b, err := st.GetBinding(ctx, state.KindTarget, t)
			if err != nil {
				return "", false
			}
			boot, ok := drv.Bootstrap(driver.GuestID(b.Ref))
			return boot.Token, ok
		}
		go linktest.New(lk.Handler(), token).Run(ctx)
	}
	if _, err := st.Head(ctx); err == nil {
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
	return nil
}
