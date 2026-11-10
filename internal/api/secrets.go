package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/store"
)

// The secret operations (D-087). No handler here logs a request body, and
// no error text holds a value: the body of PUT is read by secretBody,
// which answers with fixed texts only.

type secretView struct {
	Name      string    `json:"name"`
	Version   int64     `json:"version"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
	UsedBy    []string  `json:"used_by"`
}

func viewSecret(i secret.Info) secretView {
	used := i.UsedBy
	if used == nil {
		used = []string{}
	}
	return secretView{Name: i.Name, Version: i.Version, UpdatedBy: i.UpdatedBy, UpdatedAt: i.UpdatedAt.UTC(), UsedBy: used}
}

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request, _ *principal) {
	n, err := limit(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	after, err := decodeCursor(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	all, err := s.cfg.Secrets.List(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	items := []secretView{}
	var next *string
	for _, i := range all { // sorted by name
		if after != "" && i.Name <= after {
			continue
		}
		if len(items) == n {
			next = encodeCursor(items[len(items)-1].Name)
			break
		}
		items = append(items, viewSecret(i))
	}
	writeJSON(w, http.StatusOK, page{Items: items, Next: next})
}

// maxSecretBody bounds the PUT body: a 64 KiB value escaped as \u00XX is
// six times as long, plus the JSON around it.
const maxSecretBody = 6*secret.MaxValueBytes + 1024

// secretBody reads {"value": "..."}. The answers never echo any part of
// the body, also not a single character of a syntax error.
func secretBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSecretBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			problem(w, http.StatusRequestEntityTooLarge, "too-large", "Request too large", "The body is larger than the largest secret value allows.")
			return "", false
		}
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "The body could not be read.")
		return "", false
	}
	var req struct {
		Value *string `json:"value"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", `The body must be one JSON object {"value": "<text>"} and nothing else.`)
		return "", false
	}
	if req.Value == nil {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed", Status: http.StatusUnprocessableEntity,
			Detail: "value is required.", Errors: []fieldError{{Pointer: "/value", Message: "required"}}})
		return "", false
	}
	// encoding/json silently replaces invalid UTF-8 and lone surrogates
	// with U+FFFD. A value must arrive exactly as sent, so refuse both.
	if !utf8.Valid(raw) || strings.Count(*req.Value, "�") > replacementsSent(raw) || !secret.ValidValue(*req.Value) {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed", Status: http.StatusUnprocessableEntity,
			Detail: "value must be UTF-8 text of 1 to 65536 bytes.", Errors: []fieldError{{Pointer: "/value", Message: "UTF-8 text of 1 to 65536 bytes"}}})
		return "", false
	}
	return *req.Value, true
}

// replacementsSent counts the U+FFFD characters that the client really
// sent: literal or as the escape �.
func replacementsSent(raw []byte) int {
	return bytes.Count(raw, []byte("�")) + bytes.Count(bytes.ToLower(raw), []byte(`�`))
}

func (s *Server) putSecret(w http.ResponseWriter, r *http.Request, p *principal) {
	name := r.PathValue("name")
	if !secret.ValidName(name) {
		problem(w, http.StatusUnprocessableEntity, "validation", "Validation failed", secret.ErrInvalidName.Error())
		return
	}
	value, ok := secretBody(w, r)
	if !ok {
		return
	}
	info, err := s.cfg.Secrets.Put(r.Context(), name, value, p.name)
	if err != nil {
		s.internal(w, err)
		return
	}
	if s.cfg.Events != nil {
		s.cfg.Events.Emit(r.Context(), "secret.updated", "secret:"+name, p.name, map[string]any{"name": name})
	}
	writeJSON(w, http.StatusOK, viewSecret(info))
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request, p *principal) {
	name := r.PathValue("name")
	err := s.cfg.Secrets.Delete(r.Context(), name)
	var inUse *secret.InUseError
	switch {
	case errors.As(err, &inUse):
		targets, plans := inUse.Targets, inUse.Plans
		if targets == nil {
			targets = []string{}
		}
		if plans == nil {
			plans = []int64{}
		}
		writeProblem(w, problemBody{Type: "urn:plumb:problem:secret-in-use", Title: "Secret is in use", Status: http.StatusConflict,
			Detail:  fmt.Sprintf("Secret %q is used by %d target(s) and %d pending Plan(s).", name, len(inUse.Targets), len(inUse.Plans)),
			Targets: targets, Plans: plans})
		return
	case errors.Is(err, store.ErrNotFound):
		problem(w, http.StatusNotFound, "not-found", "Not found", fmt.Sprintf("Secret %q does not exist.", name))
		return
	case err != nil:
		s.internal(w, err)
		return
	}
	if s.cfg.Events != nil {
		s.cfg.Events.Emit(r.Context(), "secret.deleted", "secret:"+name, p.name, map[string]any{"name": name})
	}
	w.WriteHeader(http.StatusNoContent)
}
