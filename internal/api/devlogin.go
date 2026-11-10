package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/Bernotese/plumb/internal/store"
)

// DevUser is the user of the dev login (D-080). It has no usable password.
const DevUser = "dev"

// devLogin makes a normal session for the dev user and redirects to a
// local path (D-080). A wrong or missing token is 404, like a server
// without the route. This is the one exception to the Origin rule of D-070:
// browser-shot opens the link without an Origin header.
func (s *Server) devLogin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("token")), []byte(s.cfg.DevLoginToken)) != 1 {
		http.NotFound(w, r)
		return
	}
	u, err := s.st.UserByName(r.Context(), DevUser)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	id := randomText(32)
	sess := store.Session{IDHash: sha256Hex(id), UserID: u.ID, ExpiresAt: s.now().Add(s.cfg.SessionTTL)}
	if err := s.st.CreateSession(r.Context(), sess); err != nil {
		s.internal(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: id, Path: "/", Expires: sess.ExpiresAt,
		HttpOnly: true, Secure: !s.cfg.Dev, SameSite: http.SameSiteStrictMode})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer") // the token is in this URL
	http.Redirect(w, r, localPath(q.Get("next")), http.StatusSeeOther)
}

// localPath returns next when it is a path on this server: one leading
// slash, no "//", no backslash, no control character. Else "/".
func localPath(next string) string {
	if !strings.HasPrefix(next, "/") || strings.Contains(next, "//") || strings.Contains(next, "\\") {
		return "/"
	}
	for _, c := range next {
		if c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	return next
}
