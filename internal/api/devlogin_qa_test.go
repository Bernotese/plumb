package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestProductionTLSDevLoginIsAbsentWithUI(t *testing.T) {
	e := newEnv(t, false)
	e.srv.cfg.DevLoginToken = "must-not-enable-production-login"
	e.srv.cfg.UI = fstest.MapFS{"index.html": {Data: []byte("<!doctype html><div id=app></div>")}}
	srv := httptest.NewTLSServer(e.srv.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/dev/login?token=must-not-enable-production-login&next=/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || len(resp.Cookies()) != 0 {
		t.Fatalf("production TLS server exposed /dev/login: status=%d cookies=%d", resp.StatusCode, len(resp.Cookies()))
	}
}
