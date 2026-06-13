// Package identity is the deliberate stub for clinician identity.
// The skeleton trusts an X-Actor-HPI header against a fixed list of
// synthetic clinicians. Swapping this for OIDC/My Health Account
// Workforce later means replacing Middleware only — nothing else in
// the system knows where the Actor came from.
package identity

import (
	"context"
	"net/http"

	"github.com/sekickivuk-hue/nz-open-emr/internal/fhir"
)

type Actor struct {
	HPI  string `json:"hpi"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// Demo actors use the 99ZZZx synthetic HPI range.
var Demo = []Actor{
	{HPI: "99ZZZA", Name: "Dr Aroha Demo", Role: "SMO General Medicine"},
	{HPI: "99ZZZB", Name: "Dr Ben Demo", Role: "General Practitioner"},
	{HPI: "99ZZZC", Name: "RN Cath Demo", Role: "Registered Nurse"},
}

func Lookup(hpi string) (Actor, bool) {
	for _, a := range Demo {
		if a.HPI == hpi {
			return a, true
		}
	}
	return Actor{}, false
}

type ctxKey struct{}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := Lookup(r.Header.Get("X-Actor-HPI"))
		if !ok {
			fhir.WriteError(w, http.StatusUnauthorized, "login",
				"missing or unknown X-Actor-HPI header (demo identity)")
			return
		}
		next.ServeHTTP(w, r.WithContext(
			context.WithValue(r.Context(), ctxKey{}, actor)))
	})
}

func FromContext(ctx context.Context) Actor {
	a, _ := ctx.Value(ctxKey{}).(Actor)
	return a
}
