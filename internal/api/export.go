package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/secret"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// Export and import (D-094, D-095). The passphrase and the plaintext of a
// bundle are never logged and never in an error text.

// exportFile builds the export of the head revision. bundle may be nil.
func (s *Server) exportFile(ctx context.Context, bundle *secret.Bundle) ([]byte, int64, error) {
	head, err := s.st.Head(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.st.RevisionDocs(ctx, head.ID)
	if err != nil {
		return nil, 0, err
	}
	docs := make([]state.Doc, len(rows))
	for i, r := range rows {
		if err := json.Unmarshal(r.Body, &docs[i]); err != nil {
			return nil, 0, err
		}
	}
	infos, err := s.cfg.Secrets.List(ctx)
	if err != nil {
		return nil, 0, err
	}
	names := make([]string, len(infos))
	for i, in := range infos {
		names[i] = in.Name
	}
	h := state.ExportHeader{Format: state.ExportFormat, Revision: head.ID, ExportedAt: s.now(), Version: s.cfg.Version, Secrets: names}
	var buf bytes.Buffer
	var b any
	if bundle != nil {
		b = bundle
	}
	if err := state.Encode(&buf, h, docs, b); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), head.ID, nil
}

func writeYAML(w http.ResponseWriter, body []byte, rev int64) {
	h := w.Header()
	h.Set("Content-Type", "application/yaml")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="plumb-export-r%d.yaml"`, rev))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func (s *Server) noRevision(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		problem(w, http.StatusNotFound, "not-found", "Not found", "There is no revision yet, so there is nothing to export.")
		return
	}
	s.internal(w, err)
}

func (s *Server) exportState(w http.ResponseWriter, r *http.Request, _ *principal) {
	body, rev, err := s.exportFile(r.Context(), nil)
	if err != nil {
		s.noRevision(w, err)
		return
	}
	writeYAML(w, body, rev)
}

type exportRequest struct {
	Passphrase string `json:"passphrase"`
}

// minPassphrase is the shortest passphrase of a bundle, in characters (D-094).
const minPassphrase = 12

func (s *Server) exportStateWithSecrets(w http.ResponseWriter, r *http.Request, _ *principal) {
	var req exportRequest
	if !decode(w, r, &req, false) {
		return
	}
	if n := utf8.RuneCountInString(req.Passphrase); n < minPassphrase || len(req.Passphrase) > 1024 {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed", Status: http.StatusUnprocessableEntity,
			Errors: []fieldError{{"/passphrase", "A passphrase of 12 to 1024 characters is required."}}})
		return
	}
	values, err := s.cfg.Secrets.All(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	var bundle secret.Bundle
	var serr error
	if !s.withHashSlot(func() { bundle, serr = secret.SealBundle(values, req.Passphrase, secret.BundleKDF) }) {
		tooMany(w, time.Second)
		return
	}
	for k := range values {
		delete(values, k)
	}
	if serr != nil {
		s.internal(w, serr)
		return
	}
	body, rev, err := s.exportFile(r.Context(), &bundle)
	if err != nil {
		s.noRevision(w, err)
		return
	}
	writeYAML(w, body, rev)
}

type importRequest struct {
	Content    string   `json:"content"`
	Mode       string   `json:"mode"`
	Passphrase string   `json:"passphrase"`
	Overwrite  []string `json:"overwrite"`
	Reason     string   `json:"reason"`
}

// maxImportBody is the largest import request: an 8 MiB file in a JSON
// string, plus room for escapes and the other fields. The file itself is
// checked against 8 MiB after decoding.
const maxImportBody = 2*state.MaxExportBytes + 64<<10

// secretConflict is the 409 of an import (D-095).
type secretConflict struct{ names []string }

func (e *secretConflict) Error() string {
	return "import: secrets exist with another value: " + strings.Join(e.names, ", ")
}

func invalid(w http.ResponseWriter, pointer, msg string) {
	writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed", Status: http.StatusUnprocessableEntity,
		Detail: msg, Errors: []fieldError{{pointer, msg}}})
}

func (s *Server) importState(w http.ResponseWriter, r *http.Request, p *principal) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBody))
	if err != nil {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "The body is larger than an import allows (8 MiB of file).")
		return
	}
	var req importRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "The body must be one JSON object (ImportRequest).")
		return
	}
	switch {
	case len(req.Content) > state.MaxExportBytes:
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "The file is larger than 8 MiB.")
		return
	case req.Content == "":
		invalid(w, "/content", "The export file is empty.")
		return
	case strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 2000:
		invalid(w, "/reason", "A reason of 1 to 2000 characters is required.")
		return
	case req.Mode != "" && req.Mode != "replace" && req.Mode != "merge":
		invalid(w, "/mode", "mode is replace or merge.")
		return
	case len(req.Passphrase) > 1024:
		invalid(w, "/passphrase", "The passphrase is longer than 1024 bytes.")
		return
	}
	for i, n := range req.Overwrite {
		if !secret.ValidName(n) {
			invalid(w, fmt.Sprintf("/overwrite/%d", i), "not a secret name")
			return
		}
	}
	file, err := state.Decode([]byte(req.Content))
	var ee *state.ExportError
	if errors.As(err, &ee) {
		invalid(w, "/content", ee.Error())
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}

	// The bundle: parsed and checked before any argon2 work (D-094).
	var values map[string]string
	if req.Passphrase != "" {
		if file.Bundle == nil {
			invalid(w, "/passphrase", "The file has no SecretBundle; leave the passphrase out.")
			return
		}
		b, err := secret.ParseBundle(file.Bundle)
		if err != nil {
			invalid(w, "/content", err.Error())
			return
		}
		var oerr error
		if !s.withHashSlot(func() { values, oerr = secret.OpenBundle(b, req.Passphrase) }) {
			tooMany(w, time.Second)
			return
		}
		if oerr != nil {
			invalid(w, "/passphrase", oerr.Error())
			return
		}
	}
	for i, n := range req.Overwrite {
		if _, ok := values[n]; !ok {
			invalid(w, fmt.Sprintf("/overwrite/%d", i), fmt.Sprintf("secret %q is not in the bundle of the file", n))
			return
		}
	}
	bundled := make([]string, 0, len(values))
	for n := range values {
		bundled = append(bundled, n)
	}
	sort.Strings(bundled)

	write := func(ctx context.Context) ([]string, error) { return s.writeBundle(ctx, values, req.Overwrite, p.name) }
	pl, err := s.cfg.Plans.Import(r.Context(), plan.ImportInput{Docs: file.Docs, Replace: req.Mode != "merge",
		Reason: req.Reason, Author: p.name, Bundled: bundled, WriteSecrets: write})
	for k := range values {
		delete(values, k)
	}
	var conflict *secretConflict
	var se state.Errors
	switch {
	case errors.As(err, &conflict):
		writeProblem(w, problemBody{Type: "urn:plumb:problem:secret-conflict", Title: "Secrets exist with another value", Status: http.StatusConflict,
			Detail:  fmt.Sprintf("%d secret(s) of the file differ from the stored value. Nothing was written. Name them in \"overwrite\" to replace them.", len(conflict.names)),
			Secrets: conflict.names})
		return
	case errors.As(err, &se): // through ProposalError.Unwrap too
		s.importErrors(w, file, se)
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}

// importErrors writes the document errors of an import. A pointer into
// the file text is not possible, so every error points at /content and
// names the document by its place in the file (0 is the header).
func (s *Server) importErrors(w http.ResponseWriter, file *state.ExportFile, se state.Errors) {
	index := map[[2]string]int{}
	for i, d := range file.Docs {
		index[[2]string{d.Kind, d.Name}] = i + 1
	}
	var fe []fieldError
	for _, e := range se {
		at := "?"
		if i, ok := index[[2]string{e.Kind, e.Name}]; ok {
			at = fmt.Sprint(i)
		}
		fe = append(fe, fieldError{Pointer: "/content", Message: fmt.Sprintf("document %s (%s %q) %s: %s", at, e.Kind, e.Name, e.Path, e.Msg)})
	}
	writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed",
		Status: http.StatusUnprocessableEntity, Detail: "The documents of the file are not valid.", Errors: fe})
}

// writeBundle writes the secrets of a bundle (D-095): a missing name is
// created, an equal value is skipped (no new version), another value is a
// conflict unless overwrite names it. On a conflict nothing is written.
// It returns the warnings of the Plan.
func (s *Server) writeBundle(ctx context.Context, values map[string]string, overwrite []string, by string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	have, err := s.cfg.Secrets.Versions(ctx, names)
	if err != nil {
		return nil, err
	}
	var existing []string
	for _, n := range names {
		if _, ok := have[n]; ok {
			existing = append(existing, n)
		}
	}
	current, err := s.cfg.Secrets.Resolve(ctx, existing)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, n := range overwrite {
		allowed[n] = true
	}
	var create, replace, conflicts []string
	for _, n := range names {
		cur, exists := current[n]
		switch {
		case !exists:
			create = append(create, n)
		case cur.Text == values[n]:
		case allowed[n]:
			replace = append(replace, n)
		default:
			conflicts = append(conflicts, n)
		}
	}
	for k := range current {
		delete(current, k)
	}
	if len(conflicts) > 0 {
		return nil, &secretConflict{names: conflicts}
	}
	var notes []string
	for _, n := range create {
		if _, err := s.cfg.Secrets.Put(ctx, n, values[n], by); err != nil {
			return nil, err
		}
		s.emitSecret(ctx, n, by)
		notes = append(notes, fmt.Sprintf("The import created secret %q.", n))
	}
	for _, n := range replace {
		if _, err := s.cfg.Secrets.Put(ctx, n, values[n], by); err != nil {
			return nil, err
		}
		s.emitSecret(ctx, n, by)
		if err := s.cfg.Plans.SecretChanged(ctx, n); err != nil {
			return nil, err
		}
		notes = append(notes, fmt.Sprintf("The import replaced the value of secret %q.", n))
	}
	return notes, nil
}

func (s *Server) emitSecret(ctx context.Context, name, by string) {
	if s.cfg.Events != nil {
		s.cfg.Events.Emit(ctx, "secret.updated", "secret:"+name, by, map[string]any{"name": name})
	}
}
