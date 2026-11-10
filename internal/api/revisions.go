package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
)

type revisionInfo struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"`
	Reason    string    `json:"reason"`
	PlanID    *int64    `json:"plan_id"`
	CreatedAt time.Time `json:"created_at"`
	Documents int       `json:"documents"`
}

func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request, _ *principal) {
	n, err := limit(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	before, err := idCursor(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	revs, err := s.st.Revisions(r.Context(), before, n+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	items := []revisionInfo{}
	var next *string
	for i, rev := range revs {
		if i == n {
			next = encodeCursor(strconv.FormatInt(revs[n-1].ID, 10))
			break
		}
		count, err := s.st.DocCount(r.Context(), rev.ID)
		if err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, revisionInfo{ID: rev.ID, Author: rev.Author, Reason: rev.Reason, PlanID: nullID(rev.PlanID),
			CreatedAt: rev.CreatedAt.UTC(), Documents: count})
	}
	writeJSON(w, http.StatusOK, page{Items: items, Next: next})
}

// revisionN reads the path value n. A value that is no revision number is
// 404, like an unknown revision.
func revisionN(w http.ResponseWriter, r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue("n"), 10, 64)
	if err != nil || n < 1 {
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such revision.")
		return 0, false
	}
	return n, true
}

type documentChange struct {
	Kind   string          `json:"kind"`
	Name   string          `json:"name"`
	Op     store.DiffOp    `json:"op"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

type revisionDiff struct {
	From    *int64           `json:"from"`
	To      int64            `json:"to"`
	Changes []documentChange `json:"changes"`
}

func (s *Server) diffRevision(w http.ResponseWriter, r *http.Request, _ *principal) {
	n, ok := revisionN(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := s.st.Revision(ctx, n); err != nil {
		s.notFoundRevision(w, err, n)
		return
	}
	from := n - 1
	if v := r.URL.Query().Get("from"); v != "" {
		m, err := strconv.ParseInt(v, 10, 64)
		if err != nil || m < 1 {
			badQuery(w, errors.New("from must be a revision number"))
			return
		}
		if _, err := s.st.Revision(ctx, m); err != nil {
			s.notFoundRevision(w, err, m)
			return
		}
		from = m
	}
	changes, err := s.st.Diff(ctx, from, n) // from 0 is the empty state
	if err != nil {
		s.internal(w, err)
		return
	}
	out := revisionDiff{From: nullID(from), To: n, Changes: []documentChange{}}
	body := func(hash string) (json.RawMessage, error) {
		if hash == "" {
			return json.RawMessage("null"), nil
		}
		d, err := s.st.Doc(ctx, hash)
		return json.RawMessage(d.Body), err
	}
	for _, c := range changes {
		dc := documentChange{Kind: c.Kind, Name: c.Name, Op: c.Op}
		if dc.Before, err = body(c.From); err == nil {
			dc.After, err = body(c.To)
		}
		if err != nil {
			s.internal(w, err)
			return
		}
		out.Changes = append(out.Changes, dc)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) notFoundRevision(w http.ResponseWriter, err error, n int64) {
	if errors.Is(err, store.ErrNotFound) {
		problem(w, http.StatusNotFound, "not-found", "Not found", fmt.Sprintf("Revision %d does not exist.", n))
		return
	}
	s.internal(w, err)
}

func (s *Server) rollbackRevision(w http.ResponseWriter, r *http.Request, p *principal) {
	n, ok := revisionN(w, r)
	if !ok {
		return
	}
	var req reasonRequest
	if !decode(w, r, &req, false) {
		return
	}
	if strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 2000 {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed", Status: http.StatusUnprocessableEntity,
			Errors: []fieldError{{"/reason", "A reason of 1 to 2000 characters is required."}}})
		return
	}
	pl, err := s.cfg.Plans.Rollback(r.Context(), n, req.Reason, p.name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		problem(w, http.StatusNotFound, "not-found", "Not found", fmt.Sprintf("Revision %d does not exist.", n))
		return
	case errors.Is(err, plan.ErrNothingToDo):
		problem(w, http.StatusConflict, "nothing-to-do", "Nothing to do", fmt.Sprintf("Revision %d has the same documents as the head revision.", n))
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}
