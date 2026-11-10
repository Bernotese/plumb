package api

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSSEResumeAcrossBatches(t *testing.T) {
	for _, header := range []bool{false, true} {
		t.Run(strconv.FormatBool(header), func(t *testing.T) {
			e := newEnv(t, true)
			reader := e.token(TierRead, "")
			for range 205 {
				if _, err := e.events.Emit(e.ctx, "plan.created", "plan:1", "system", nil); err != nil {
					t.Fatal(err)
				}
			}
			query, last := "?since=0", ""
			if header {
				query, last = "", "0"
			}
			s := e.stream(reader, query, last)
			for want := int64(1); want <= 205; want++ {
				if ev := s.nextEvent(t); ev.id != itoa(want) || ev.data.Seq != want {
					t.Fatalf("replay got %+v, want seq %d", ev, want)
				}
			}
			s.resp.Body.Close()
			if _, err := e.events.Emit(e.ctx, "plan.stale", "plan:1", "system", nil); err != nil {
				t.Fatal(err)
			}
			reconnected := e.stream(reader, "", "205")
			if ev := reconnected.nextEvent(t); ev.id != "206" || ev.typ != "plan.stale" {
				t.Fatalf("reconnect duplicated cursor or lost successor: %+v", ev)
			}
			if _, err := e.events.Emit(e.ctx, "issue.opened", "issue:1", "system", nil); err != nil {
				t.Fatal(err)
			}
			if ev := reconnected.nextEvent(t); ev.id != "207" || ev.typ != "issue.opened" {
				t.Fatalf("live continuation: %+v", ev)
			}
		})
	}
}

func TestSSEInvalidResumeMatchesContract(t *testing.T) {
	e := newEnv(t, true)
	reader := e.token(TierRead, "")
	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+reader.bearer)
	req.Header.Set("Last-Event-ID", "not-a-sequence")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 400 || problemType(rec.Body.Bytes()) != "bad-request" {
		t.Fatalf("invalid resume: %d %s", rec.Code, rec.Body)
	}
	ctype, _, _ := strings.Cut(rec.Header().Get("Content-Type"), ";")
	loadContract(t).check(t, recorded{"GET", "/events", rec.Code, ctype, rec.Body.Bytes()})
}
