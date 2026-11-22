package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBasicAuth(t *testing.T) {
	tests := []struct {
		name       string
		user, pass string
		setHeader  bool
		wantDenied bool
	}{
		{name: "no header", wantDenied: true},
		{name: "wrong password", user: "admin", pass: "wrong", setHeader: true, wantDenied: true},
		{name: "wrong username", user: "root", pass: "secret", setHeader: true, wantDenied: true},
		{name: "right values", user: "admin", pass: "secret", setHeader: true},
	}
	for _, path := range []string{"/", "/api/x"} {
		for _, tt := range tests {
			t.Run(path+" "+tt.name, func(t *testing.T) {
				s := New(Options{Username: "admin", Password: "secret"})
				r := httptest.NewRequest(http.MethodGet, path, nil)
				if tt.setHeader {
					r.SetBasicAuth(tt.user, tt.pass)
				}
				rec := serve(s, r)

				if !tt.wantDenied {
					if rec.Code == http.StatusUnauthorized {
						t.Fatal("status = 401 with the right values")
					}
					return
				}
				assertJSONError(t, rec, http.StatusUnauthorized, "unauthorized")
				want := `Basic realm="autorefresh", charset="UTF-8"`
				if got := rec.Header().Get("WWW-Authenticate"); got != want {
					t.Errorf("WWW-Authenticate = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestNoAuthWhenUnset(t *testing.T) {
	rec := serve(New(Options{}), httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate = %q, want none", got)
	}
}
