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

// Extension is a FHIR extension (url + value).
type Extension struct {
	URL           string        `json:"url"`
	ValueCodeableConcept *CodeableConcept `json:"valueCodeableConcept,omitempty"`
	ValueString   *string       `json:"valueString,omitempty"`
	Extension     []Extension   `json:"extension,omitempty"` // nested for complex extensions
}

// CodeableConcept is a coded value with optional display text.
type CodeableConcept struct {
	Coding []Coding `json:"coding,omitempty"`
	Text   string   `json:"text,omitempty"`
}

// Coding is a reference to a code in a terminology system.
type Coding struct {
	System  string `json:"system,omitempty"`
	Code    string `json:"code,omitempty"`
	Display string `json:"display,omitempty"`
}

// NZ FHIR extension URLs (from FHIR NZ Base IG & NHI IG).
const (
	ExtEthnicity   = "https://hl7.org.nz/fhir/StructureDefinition/nz-ethnicity"
	ExtNZCitizen   = "https://hl7.org.nz/fhir/StructureDefinition/nz-citizenship"
	ExtBirthPlace  = "https://hl7.org.nz/fhir/StructureDefinition/birth-place"
	ExtDHB         = "https://hl7.org.nz/fhir/StructureDefinition/dhb"
	ExtIwi         = "https://hl7.org.nz/fhir/StructureDefinition/nz-iwi"
	ExtNameInfoSrc = "https://hl7.org.nz/fhir/StructureDefinition/nz-name-information-source"
)

// EthnicityCodes returns the ethnicity Coding values from extensions, or nil.
func EthnicityCodes(exts []Extension) []Coding {
	var out []Coding
	for _, e := range exts {
		if e.URL == ExtEthnicity && e.ValueCodeableConcept != nil {
			out = append(out, e.ValueCodeableConcept.Coding...)
		}
	}
	return out
}

// NZCitizenship extracts citizenship status and source from extensions.
func NZCitizenship(exts []Extension) (status, source string) {
	for _, e := range exts {
		if e.URL == ExtNZCitizen {
			for _, inner := range e.Extension {
				switch inner.URL {
				case "status":
					if inner.ValueCodeableConcept != nil && len(inner.ValueCodeableConcept.Coding) > 0 {
						status = inner.ValueCodeableConcept.Coding[0].Code
					}
				case "source":
					if inner.ValueCodeableConcept != nil && len(inner.ValueCodeableConcept.Coding) > 0 {
						source = inner.ValueCodeableConcept.Coding[0].Code
					}
				}
			}
		}
	}
	return
}

// BirthPlace extracts birth place from extensions.
func BirthPlace(exts []Extension) (country, place string) {
	for _, e := range exts {
		if e.URL == ExtBirthPlace {
			for _, inner := range e.Extension {
				switch inner.URL {
				case "country":
					if inner.ValueCodeableConcept != nil && len(inner.ValueCodeableConcept.Coding) > 0 {
						country = inner.ValueCodeableConcept.Coding[0].Code
					}
				case "place":
					if inner.ValueString != nil {
						place = *inner.ValueString
					}
				}
			}
		}
	}
	return
}

// DHB extracts DHB code from extensions.
func DHB(exts []Extension) string {
	for _, e := range exts {
		if e.URL == ExtDHB && e.ValueCodeableConcept != nil && len(e.ValueCodeableConcept.Coding) > 0 {
			return e.ValueCodeableConcept.Coding[0].Code
		}
	}
	return ""
}

// IwiCodes extracts iwi codes from extensions.
func IwiCodes(exts []Extension) []string {
	var out []string
	for _, e := range exts {
		if e.URL == ExtIwi && e.ValueCodeableConcept != nil {
			for _, c := range e.ValueCodeableConcept.Coding {
				out = append(out, c.Code)
			}
		}
	}
	return out
}

type Patient struct {
	ResourceType string       `json:"resourceType"`
	ID           string       `json:"id,omitempty"`
	Extension    []Extension  `json:"extension,omitempty"`
	Identifier   []Identifier `json:"identifier,omitempty"`
	Name         []HumanName  `json:"name,omitempty"`
	Gender       string       `json:"gender,omitempty"`
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

// --- Encounter ------------------------------------------------------------

type EncounterFHIR struct {
	ResourceType string       `json:"resourceType"`
	ID           string       `json:"id,omitempty"`
	Status       string       `json:"status"` // in-progress | finished
	Class        Coding       `json:"class"`
	Subject      *Reference   `json:"subject,omitempty"`
	Period       *Period      `json:"period,omitempty"`
	Diagnosis    []EncDiag    `json:"diagnosis,omitempty"`
	Extension    []Extension  `json:"extension,omitempty"`
}

type Period struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type EncDiag struct {
	Condition  *Reference     `json:"condition,omitempty"`
	Use        *CodeableConcept `json:"use,omitempty"`
	Rank       int            `json:"rank,omitempty"`
}

func (e EncounterFHIR) MarshalJSON() ([]byte, error) {
	type alias EncounterFHIR
	a := alias(e)
	a.ResourceType = "Encounter"
	return json.Marshal(a)
}

// --- AllergyIntolerance ---------------------------------------------------

type AllergyIntoleranceFHIR struct {
	ResourceType string          `json:"resourceType"`
	ID           string          `json:"id,omitempty"`
	ClinicalStatus *CodeableConcept `json:"clinicalStatus,omitempty"`
	Code         *CodeableConcept `json:"code,omitempty"`
	Patient      *Reference      `json:"patient,omitempty"`
	RecordedDate string          `json:"recordedDate,omitempty"`
	Recorder     *Reference      `json:"recorder,omitempty"`
	Reaction     []AllergyReaction `json:"reaction,omitempty"`
	Extension    []Extension     `json:"extension,omitempty"`
}

type AllergyReaction struct {
	Substance  *CodeableConcept `json:"substance,omitempty"`
	Manifestation []CodeableConcept `json:"manifestation,omitempty"`
	Severity   string           `json:"severity,omitempty"`
}

func (a AllergyIntoleranceFHIR) MarshalJSON() ([]byte, error) {
	type alias AllergyIntoleranceFHIR
	x := alias(a)
	x.ResourceType = "AllergyIntolerance"
	return json.Marshal(x)
}

// --- Condition (problem list) ---------------------------------------------

type ConditionFHIR struct {
	ResourceType  string          `json:"resourceType"`
	ID            string          `json:"id,omitempty"`
	ClinicalStatus *CodeableConcept `json:"clinicalStatus,omitempty"`
	Code          *CodeableConcept `json:"code,omitempty"`
	Subject       *Reference      `json:"subject,omitempty"`
	OnsetDateTime string          `json:"onsetDateTime,omitempty"`
	RecordedDate  string          `json:"recordedDate,omitempty"`
	Category      []CodeableConcept `json:"category,omitempty"`
	Extension     []Extension     `json:"extension,omitempty"`
}

func (c ConditionFHIR) MarshalJSON() ([]byte, error) {
	type alias ConditionFHIR
	a := alias(c)
	a.ResourceType = "Condition"
	return json.Marshal(a)
}

// --- CareTeam (simplified) ------------------------------------------------

type CareTeamFHIR struct {
	ResourceType string             `json:"resourceType"`
	ID           string             `json:"id,omitempty"`
	Status       string             `json:"status,omitempty"`
	Subject      *Reference         `json:"subject,omitempty"`
	Participant  []CareTeamParticipant `json:"participant,omitempty"`
}

type CareTeamParticipant struct {
	Role   *CodeableConcept `json:"role,omitempty"`
	Member *Reference       `json:"member,omitempty"`
	Period *Period          `json:"period,omitempty"`
}

func (c CareTeamFHIR) MarshalJSON() ([]byte, error) {
	type alias CareTeamFHIR
	a := alias(c)
	a.ResourceType = "CareTeam"
	return json.Marshal(a)
}
