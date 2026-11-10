package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/api"
	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/event"
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
		// A Block that comes after a resource of another Block and
		// restarts its service (D-167, D-172).
		d(state.KindBlock, "nginx-site", `{"title":"Nginx site","description":"One more Nginx site. Use it with the nginx Block.",`+
			`"params":{"type":"object","properties":{`+
			`"name":{"type":"string","title":"Site name","default":"site","pattern":"^[a-z0-9][a-z0-9._-]{0,63}$"},`+
			`"server_name":{"type":"string","title":"Server name","default":"site.lan"}}},`+
			`"after":["package:nginx"],"restarts":["service:nginx"],"resources":[`+
			`{"file":{"path":"/etc/nginx/sites-available/{{ .name }}","content":"server { listen 80; server_name {{ .server_name }}; }\n"}},`+
			`{"symlink":{"path":"/etc/nginx/sites-enabled/{{ .name }}","target":"/etc/nginx/sites-available/{{ .name }}"}}]}`),
	}
	for _, t := range devTargets {
		site := ""
		if t == "web2" { // one Target shows the order between Blocks; the e2e tests edit web1
			site = `,{"use":"nginx-site","id":"site-shop","params":{"name":"shop","server_name":"shop.` + t + `.lan"}}`
		}
		docs = append(docs, d(state.KindTarget, t, `{"type":"vm","image":"ubuntu-26.04",`+
			`"guest":{"cores":2,"memory":2048,"disk":20,"bridge":"vmbr0"},`+
			`"blocks":[{"use":"motd","params":{"text":"Hello from `+t+`"}},{"use":"nginx","params":{"server_name":"`+t+`.lan"}}`+site+`]}`))
	}
	return docs
}

// devAgents runs one simulated agent per bound target of --dev: the seed
// targets and every target created through the API (T-024). It follows
// the target bindings: a new binding starts an agent, a removed or
// replaced binding (new guest ref) stops it.
type devAgents struct {
	st  *store.Store
	lk  *link.Link
	drv *fake.Driver
	log *slog.Logger

	mu     sync.Mutex
	agents map[string]*devAgent
	wg     sync.WaitGroup
}

type devAgent struct {
	ref    string // the guest the agent stands for
	a      *linktest.Agent
	cancel context.CancelFunc
}

func newDevAgents(st *store.Store, lk *link.Link, drv *fake.Driver, log *slog.Logger) *devAgents {
	return &devAgents{st: st, lk: lk, drv: drv, log: log, agents: map[string]*devAgent{}}
}

// get returns the agent of a target, or nil.
func (d *devAgents) get(target string) *linktest.Agent {
	d.mu.Lock()
	defer d.mu.Unlock()
	if a := d.agents[target]; a != nil {
		return a.a
	}
	return nil
}

// run syncs the agents on every event and every tick until ctx ends.
// Then it stops all agents and waits for them.
func (d *devAgents) run(ctx context.Context, events *event.Log) {
	defer d.wg.Wait()
	defer d.stopAll()
	after, err := d.st.LastEventSeq(ctx)
	if err != nil && ctx.Err() == nil {
		d.log.Error("dev agents", "err", err)
	}
	sub := events.Subscribe(ctx, after)
	tick := time.NewTicker(time.Second) // fallback; events are the trigger
	defer tick.Stop()
	for {
		if err := d.sync(ctx); err != nil && ctx.Err() == nil {
			d.log.Error("dev agents", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case _, ok := <-sub.C:
			if !ok {
				if ctx.Err() == nil {
					d.log.Error("dev agents: event stream closed", "err", sub.Err())
				}
				sub = &event.Subscription{} // nil channel: rely on the tick
			}
		case <-tick.C:
		}
	}
}

// sync starts an agent for each bound target and stops the others.
func (d *devAgents) sync(ctx context.Context) error {
	bs, err := d.st.Bindings(ctx, state.KindTarget)
	if err != nil {
		return err
	}
	bound := map[string]string{} // target → guest ref
	for _, b := range bs {
		bound[b.Name] = b.Ref
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for t, a := range d.agents {
		if ref, ok := bound[t]; !ok || ref != a.ref {
			a.cancel()
			delete(d.agents, t)
			d.log.Info("dev agent stopped", "target", t)
		}
	}
	for t, ref := range bound {
		if d.agents[t] == nil {
			d.start(ctx, t, ref)
			d.log.Info("dev agent started", "target", t)
		}
	}
	return nil
}

// start runs a simulated agent for the guest ref of target. d.mu is held.
func (d *devAgents) start(ctx context.Context, target, ref string) {
	token := func() (string, bool) {
		boot, ok := d.drv.Bootstrap(driver.GuestID(ref))
		return boot.Token, ok
	}
	a := linktest.New(d.lk.Handler(), token)
	actx, cancel := context.WithCancel(ctx)
	d.agents[target] = &devAgent{ref: ref, a: a, cancel: cancel}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		a.Run(actx)
	}()
}

func (d *devAgents) stopAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for t, a := range d.agents {
		a.cancel()
		delete(d.agents, t)
	}
}

// seedDev plans and approves the seed documents on an empty store
// (D-039). With demo it adds the demo data of D-080: one open drift Issue
// and one pending Plan. agents must run.
func seedDev(ctx context.Context, st *store.Store, eng *plan.Engine, lk *link.Link, agents *devAgents, demo bool, log *slog.Logger) error {
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
func seedDemo(ctx context.Context, st *store.Store, eng *plan.Engine, lk *link.Link, agents *devAgents, log *slog.Logger) error {
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
	// Two more drift Issues for the ignore actions (T-036 e2e): someone
	// edits the nginx site of web1 and removes its nginx package. They are
	// on web1 because a revert plans the whole target, and the e2e reverts
	// the motd Issue of web2 (the newest Issue).
	web1 := agents.get("web1")
	if web1 == nil {
		return fmt.Errorf("dev demo: web1 has no agent")
	}
	site := web1.Guest("file:/etc/nginx/sites-enabled/default")
	if site == nil {
		return fmt.Errorf("dev demo: web1 has no nginx site")
	}
	site["content"] = "server { listen 8080; }\n"
	if err := web1.Change(ctx, "file:/etc/nginx/sites-enabled/default", site); err != nil {
		return err
	}
	if err := web1.Change(ctx, "package:nginx", nil); err != nil {
		return err
	}
	web2 := agents.get("web2")
	if web2 == nil {
		return fmt.Errorf("dev demo: web2 has no agent")
	}
	// An open drift Issue: someone edits the motd of web2.
	if err := web2.Change(ctx, "file:/etc/motd", map[string]any{
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
	// A plan token, so the token list is not empty (T-030). Its bearer is
	// thrown away: nobody can use it, and it is never printed.
	_, tok, err := api.CreateToken(ctx, st, "demo: Claude (plans and comments)", api.TierPlan, "", "user:"+api.DevUser)
	if err != nil {
		return err
	}
	log.Info("dev demo seeded", "pending_plan", p.ID, "token", tok.ID)
	return nil
}
