// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"database/sql"
	"time"
)

type PendingCharge struct {
	ReservationID, ProviderID, ProviderRequestID, PricingRevision string
	UpdatedAt                                                     time.Time
}
type Reconciler struct {
	Budget    *Budget
	Inspector ChargeInspector
}

// ChargeInspector is a deployment-qualified billing adapter. Absence of a
// definitive invoice result never releases money or repeats the original call.
type ChargeInspector interface {
	Lookup(context.Context, PendingCharge) (Usage, bool, error)
}

func (r *Reconciler) Once(ctx context.Context) (int, error) {
	if r == nil || r.Budget == nil || r.Budget.DB == nil {
		return 0, ErrUnavailable
	}
	rows, err := r.Budget.DB.QueryContext(ctx, `SELECT reservation_id,provider_id,provider_request_id,pricing_revision,updated_at FROM cap_ai.pending_model_charges()`)
	if err != nil {
		return 0, ErrUnavailable
	}
	pending := []PendingCharge{}
	for rows.Next() {
		var p PendingCharge
		var request sql.NullString
		if err = rows.Scan(&p.ReservationID, &p.ProviderID, &request, &p.PricingRevision, &p.UpdatedAt); err != nil {
			rows.Close()
			return 0, ErrUnavailable
		}
		p.ProviderRequestID = request.String
		pending = append(pending, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, ErrUnavailable
	}
	unresolved := 0
	for _, p := range pending {
		if r.Inspector == nil || p.ProviderRequestID == "" {
			unresolved++
			continue
		}
		usage, known, e := r.Inspector.Lookup(ctx, p)
		if e != nil || !known || usage.PricingRevision != p.PricingRevision {
			unresolved++
			continue
		}
		if e = r.Budget.Settle(ctx, p.ReservationID, "reconciliation", usage); e != nil {
			return unresolved, e
		}
	}
	// Return only aggregate escalation count, never prompts, source metadata or keys.
	return unresolved, nil
}
