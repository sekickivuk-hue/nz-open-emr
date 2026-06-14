// Package careteam defines care team event types and projects them.
//
// Zero dependency on core beyond eventstore.Event and projection.Register.
package careteam

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
)

const (
	TypeCareTeamMemberAdded   = "CareTeamMemberAdded"
	TypeCareTeamMemberRemoved = "CareTeamMemberRemoved"
)

type CareTeamMemberAdded struct {
	ID           string `json:"id"`
	PatientID    string `json:"patientId"`
	ClinicianHPI string `json:"clinicianHpi"`
	Role         string `json:"role"`
	StartDate    string `json:"startDate"`
}

type CareTeamMemberRemoved struct {
	MembershipID string `json:"membershipId"`
	PatientID    string `json:"patientId"`
	ClinicianHPI string `json:"clinicianHpi"`
	EndDate      string `json:"endDate"`
}

type memberAddedHandler struct{}

func (memberAddedHandler) EventTypes() []string { return []string{TypeCareTeamMemberAdded} }
func (memberAddedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var c CareTeamMemberAdded
	if err := json.Unmarshal(ev.Payload, &c); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO care_team (id, patient_id, clinician_hpi, role, start_date, last_event_seq)
		VALUES ($1,$2,$3,$4,$5::date,$6)
		ON CONFLICT (id) DO UPDATE SET
			role = EXCLUDED.role, start_date = EXCLUDED.start_date, end_date = NULL,
			last_event_seq = EXCLUDED.last_event_seq`,
		c.ID, c.PatientID, c.ClinicianHPI, c.Role, c.StartDate, ev.Seq)
	return err
}

type memberRemovedHandler struct{}

func (memberRemovedHandler) EventTypes() []string { return []string{TypeCareTeamMemberRemoved} }
func (memberRemovedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var c CareTeamMemberRemoved
	if err := json.Unmarshal(ev.Payload, &c); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE care_team SET end_date = $1::date, last_event_seq = $2
		WHERE patient_id = $3 AND clinician_hpi = $4 AND end_date IS NULL`,
		c.EndDate, ev.Seq, c.PatientID, c.ClinicianHPI)
	return err
}

func init() {
	projection.Register(memberAddedHandler{})
	projection.Register(memberRemovedHandler{})
}
