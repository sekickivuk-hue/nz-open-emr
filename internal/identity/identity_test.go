package identity_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sekickivuk-hue/nz-open-emr/internal/identity"
)

func TestMiddleware(t *testing.T) {
	var got identity.Actor
	h := identity.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = identity.FromContext(r.Context())
	}))

	// Valid actor passes and lands in context.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Actor-HPI", "99ZZZA")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || got.HPI != "99ZZZA" || got.Name == "" {
		t.Fatalf("code=%d actor=%+v", rec.Code, got)
	}

	// Missing header → 401 with OperationOutcome.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("missing header: code=%d", rec.Code)
	}

	// Unknown actor → 401.
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Actor-HPI", "11AAAA")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("unknown actor: code=%d", rec.Code)
	}
}
