// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lib/pq"
)

type ReservationRequest struct {
	OperationKey    string `json:"operation_key"`
	ActorID         string `json:"actor_id"`
	AppID           string `json:"app_id"`
	ProfileID       string `json:"profile_id"`
	ProfileRevision int64  `json:"profile_revision"`
	Estimate        int64  `json:"estimate"`
	PricingRevision string `json:"pricing_revision"`
	Fingerprint     string `json:"fingerprint"`
}
type Reservation struct {
	ID    string `json:"id"`
	State string `json:"state"`
}
type Budget struct{ DB *sql.DB }

func modelError(err error) error {
	if err == nil {
		return nil
	}
	if p, ok := errors.AsType[*pq.Error](err); ok {
		switch p.Code {
		case "P0429":
			return ErrQuota
		case "P0409":
			return ErrConflict
		case "P0403":
			return ErrPolicy
		}
	}
	return ErrUnavailable
}

func (b *Budget) Reserve(ctx context.Context, req ReservationRequest) (Reservation, error) {
	var r Reservation
	if b == nil || b.DB == nil {
		return r, ErrUnavailable
	}
	raw, _ := json.Marshal(req)
	var data []byte
	err := b.DB.QueryRowContext(ctx, `SELECT cap_ai.reserve_model($1)`, string(raw)).Scan(&data)
	if err != nil {
		return r, modelError(err)
	}
	if json.Unmarshal(data, &r) != nil {
		return r, ErrUnavailable
	}
	return r, nil
}

func (b *Budget) Settle(ctx context.Context, id, event string, usage Usage) error {
	if b == nil || b.DB == nil {
		return ErrUnavailable
	}
	raw, _ := json.Marshal(usage)
	_, err := b.DB.ExecContext(ctx, `SELECT cap_ai.settle_model($1,$2,$3)`, id, event, string(raw))
	return modelError(err)
}

func (b *Budget) begin(ctx context.Context, id string) (string, error) {
	var call string
	err := b.DB.QueryRowContext(ctx, `SELECT cap_ai.begin_model_call($1)`, id).Scan(&call)
	if err != nil {
		return "", modelError(err)
	}
	if call == "" {
		return "", ErrUncertain
	}
	return call, nil
}

func (b *Budget) finish(ctx context.Context, id, state, providerID string) error {
	_, err := b.DB.ExecContext(ctx, `SELECT cap_ai.finish_model_call($1,$2,$3)`, id, state, providerID)
	return modelError(err)
}
