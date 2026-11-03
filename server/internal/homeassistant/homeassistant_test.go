package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckPassword(t *testing.T) {
	status := http.StatusOK
	sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		if r.Method != http.MethodPost || r.URL.Path != "/auth" || r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("X-Supervisor-Token") != "sv-token" || json.NewDecoder(r.Body).Decode(&body) != nil ||
			body.Username != "anna" || body.Password != "pw" {
			t.Errorf("unexpected request %s %s %v", r.Method, r.URL, r.Header)
		}
		w.WriteHeader(status)
	}))
	defer sv.Close()
	c := New(sv.URL, "sv-token")

	for _, tt := range []struct {
		status      int
		ok, unavail bool
	}{
		{http.StatusOK, true, false},
		{http.StatusUnauthorized, false, false},
		{http.StatusForbidden, false, true},
		{http.StatusBadRequest, false, true},
		{http.StatusBadGateway, false, true},
		{http.StatusFound, false, true},
	} {
		status = tt.status
		ok, err := c.CheckPassword(context.Background(), "anna", "pw")
		if ok != tt.ok || errors.Is(err, ErrUnavailable) != tt.unavail {
			t.Errorf("status %d: ok=%v err=%v", tt.status, ok, err)
		}
	}

	sv.Close()
	if _, err := c.CheckPassword(context.Background(), "anna", "pw"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Supervisor gone: %v", err)
	}
}
