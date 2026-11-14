package link

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Bernotese/plumb/internal/link/agentinstall"
)

// RepairToken is a one-time enrollment token of a repair Run.
type RepairToken struct {
	Token   string
	Expires time.Time
	hash    string
}

// NewRepairToken creates a one-time token for a target, as
// NewEnrollToken, but the current agent stays: its credential and its
// link state work until the guest enrolls with the new token (D-385,
// review R1 of #290). The token is a live secret: it goes only into the
// repair bundle.
func (l *Link) NewRepairToken(ctx context.Context, target string) (RepairToken, error) {
	if target == "" {
		return RepairToken{}, errors.New("link: token needs a target")
	}
	t := RepairToken{Token: secret(32), Expires: l.now().Add(TokenTTL)}
	t.hash = hashSecret(t.Token)
	if err := l.cfg.Store.PutRepairToken(ctx, target, t.hash, t.Expires); err != nil {
		return RepairToken{}, err
	}
	l.emit(ctx, "target.status", target, "system", map[string]any{"agent": "repair_token_created", "expires_at": t.Expires.UTC()})
	return t, nil
}

// DropRepairToken removes the token of a repair that sent nothing, when
// no other token replaced it and the guest did not use it.
func (l *Link) DropRepairToken(ctx context.Context, target string, t RepairToken) error {
	if t.hash == "" {
		return nil
	}
	_, err := l.cfg.Store.DropEnrollToken(ctx, target, t.hash)
	return err
}

func (l *Link) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	if !l.binary.allow(r) {
		writeError(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	b := agentinstall.Script()
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}
