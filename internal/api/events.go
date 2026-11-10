package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// events streams the event log as server-sent events. It resumes after
// the `since` query or the Last-Event-ID header; without both, it sends
// only new events.
func (s *Server) events(w http.ResponseWriter, r *http.Request, _ *principal) {
	ctx := r.Context()
	var after int64 = -1
	for _, v := range []string{r.URL.Query().Get("since"), r.Header.Get("Last-Event-ID")} {
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			problem(w, http.StatusBadRequest, "bad-request", "Bad request", "since and Last-Event-ID must be a sequence number.")
			return
		}
		after = n
		break
	}
	if after < 0 {
		last, err := s.st.LastEventSeq(ctx)
		if err != nil {
			s.internal(w, err)
			return
		}
		after = last
	}
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Time{}) // a stream has no write timeout
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": plumb events\n\n")
	rc.Flush()

	sub := s.cfg.Events.Subscribe(ctx, after)
	keep := time.NewTicker(s.cfg.KeepAlive)
	defer keep.Stop()
	for {
		select {
		case e, ok := <-sub.C:
			if !ok {
				if err := sub.Err(); err != nil {
					s.log.Error("event stream", "err", err)
				}
				return
			}
			data, _ := json.Marshal(eventView{Seq: e.Seq, Type: e.Type, Time: e.Time.UTC(), Subject: e.Subject, Actor: e.Actor, Data: e.Data})
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, data); err != nil {
				return
			}
			rc.Flush()
		case <-keep.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			rc.Flush()
		case <-ctx.Done():
			return
		}
	}
}
