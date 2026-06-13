// Package fhir holds the minimal FHIR R4 resource shapes the skeleton
// speaks. These are hand-rolled on purpose: the walking skeleton proves
// the architecture, not full R4 conformance (that is a later module).
package fhir

import (
	"encoding/json"
	"net/http"
)

const (
	SystemNHI = "https://standards.digital.health.nz/ns/nhi-id"
	SystemHPI = "https://standards.digital.health.nz/ns/hpi-person-id"
	MIMEType  = "application/fhir+json"
)

type Identifier struct {
	Use    string `json:"use,omitempty"`
	System string `json:"system,omitempty"`
	Value  string `json:"value,omitempty"`
}

type HumanName struct {
	Family string   `json:"family,omitempty"`
	Given  []string `json:"given,omitempty"`
}

type Reference struct {
	Reference  string      `json:"reference,omitempty"`
	Display    string      `json:"display,omitempty"`
	Identifier *Identifier `json:"identifier,omitempty"`
}

type Patient struct {
	ResourceType string       `json:"resourceType"`
	ID           string       `json:"id,omitempty"`
	Identifier   []Identifier `json:"identifier,omitempty"`
	Name         []HumanName  `json:"name,omitempty"`
	BirthDate    string       `json:"birthDate,omitempty"`
}

func (p Patient) MarshalJSON() ([]byte, error) {
	type alias Patient
	a := alias(p)
	a.ResourceType = "Patient"
	return json.Marshal(a)
}

// NHI returns the value of the NHI identifier, or "".
func (p Patient) NHI() string {
	for _, id := range p.Identifier {
		if id.System == SystemNHI {
			return id.Value
		}
	}
	return ""
}

type Attachment struct {
	ContentType string `json:"contentType,omitempty"`
	Data        string `json:"data,omitempty"` // base64
}

type DocContent struct {
	Attachment Attachment `json:"attachment"`
}

type DocumentReference struct {
	ResourceType string       `json:"resourceType"`
	ID           string       `json:"id,omitempty"`
	Status       string       `json:"status"`
	Subject      *Reference   `json:"subject,omitempty"`
	Date         string       `json:"date,omitempty"`
	Author       []Reference  `json:"author,omitempty"`
	Content      []DocContent `json:"content"`
}

func (d DocumentReference) MarshalJSON() ([]byte, error) {
	type alias DocumentReference
	a := alias(d)
	a.ResourceType = "DocumentReference"
	if a.Status == "" {
		a.Status = "current"
	}
	return json.Marshal(a)
}

type Extension struct {
	URL         string `json:"url"`
	ValueString string `json:"valueString,omitempty"`
}

// ExtAuditHash carries the hex hash of the chained audit entry, so the
// UI can show the chain on FHIR AuditEvent resources.
const ExtAuditHash = "https://nz-open-emr.org/fhir/StructureDefinition/audit-hash"

type AuditAgent struct {
	Who       *Reference `json:"who,omitempty"`
	Requestor bool       `json:"requestor"`
}

type AuditEntity struct {
	What *Reference `json:"what,omitempty"`
}

type AuditEvent struct {
	ResourceType string        `json:"resourceType"`
	ID           string        `json:"id,omitempty"`
	Extension    []Extension   `json:"extension,omitempty"`
	Action       string        `json:"action,omitempty"`
	Recorded     string        `json:"recorded,omitempty"`
	Outcome      string        `json:"outcome,omitempty"`
	Agent        []AuditAgent  `json:"agent"`
	Entity       []AuditEntity `json:"entity,omitempty"`
}

func (a AuditEvent) MarshalJSON() ([]byte, error) {
	type alias AuditEvent
	x := alias(a)
	x.ResourceType = "AuditEvent"
	return json.Marshal(x)
}

type BundleEntry struct {
	Resource any `json:"resource"`
}

type Bundle struct {
	ResourceType string        `json:"resourceType"`
	Type         string        `json:"type"`
	Total        int           `json:"total"`
	Entry        []BundleEntry `json:"entry,omitempty"`
}

func NewSearchSet(resources []any) Bundle {
	b := Bundle{ResourceType: "Bundle", Type: "searchset", Total: len(resources)}
	for _, r := range resources {
		b.Entry = append(b.Entry, BundleEntry{Resource: r})
	}
	return b
}

type Issue struct {
	Severity    string `json:"severity"`
	Code        string `json:"code"`
	Diagnostics string `json:"diagnostics,omitempty"`
}

type OperationOutcome struct {
	ResourceType string  `json:"resourceType"`
	Issue        []Issue `json:"issue"`
}

func WriteError(w http.ResponseWriter, status int, code, diagnostics string) {
	w.Header().Set("Content-Type", MIMEType)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(OperationOutcome{
		ResourceType: "OperationOutcome",
		Issue:        []Issue{{Severity: "error", Code: code, Diagnostics: diagnostics}},
	})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", MIMEType)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
