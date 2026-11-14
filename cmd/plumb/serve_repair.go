package main

import (
	"context"
	"log/slog"
	"net"
	"net/url"

	"github.com/Bernotese/plumb/internal/api"
	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/repair"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
)

// repairService builds the SSH repair transport (T-191). The controller
// addresses of the repair line are the agent URL host as IP literals
// (review R4 of #290); an error turns repair off with a warning.
func repairService(ctx context.Context, log *slog.Logger, st *store.Store, sec *secret.Store, ev *event.Log, agentURL string,
	rewatch func(context.Context)) *repair.Service {
	u, err := url.Parse(agentURL)
	if err != nil {
		log.Warn("SSH repair is off: bad agent URL", "err", err)
		return nil
	}
	from, err := repair.FromAddrs(ctx, u.Hostname(), func(ctx context.Context, h string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, "ip", h)
	})
	if err != nil {
		log.Warn("SSH repair is off: the agent URL host has no IP address", "host", u.Hostname(), "err", err)
		from = nil
	}
	s, err := repair.New(repair.Config{Store: st, Secrets: sec, Events: ev, Logger: log, From: from, Rewatch: rewatch})
	if err != nil {
		log.Warn("SSH repair is off", "err", err)
		return nil
	}
	if err := s.EnsureKey(ctx); err != nil {
		log.Warn("SSH repair is off: no repair key", "err", err)
		return nil
	}
	return s
}

// repairConfig is the repair part of the engine config; without a
// transport a repair is not ready (409 repair-not-ready).
func repairConfig(_ context.Context, _ *slog.Logger, rep *repair.Service, lk *link.Link, agentURL string) plan.RepairConfig {
	c := plan.RepairConfig{Tokens: lk, AgentBinary: agentBinary(), CA: lk.CA().CertPEM, AgentURL: agentURL}
	if rep != nil {
		c.Transport, c.Watch, c.OnReport = rep, rep.Watch, rep.OnReport
	}
	return c
}

// repairKeys is the repair key view of the API, or nil.
func repairKeys(rep *repair.Service) api.RepairKeys {
	if rep == nil {
		return nil
	}
	return rep
}
