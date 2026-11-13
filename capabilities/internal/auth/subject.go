// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Subject is authenticated identity, without business-role or resource authority.
type Subject struct {
	UserID    string
	ExpiresAt time.Time
}

type SubjectVerifier interface {
	VerifySubject(context.Context, string) (Subject, error)
}

// NewSubjectVerifier shares the existing signature and JWKS implementation.
func NewSubjectVerifier(ctx context.Context, cfg VerifierConfig) (SubjectVerifier, error) {
	v, err := NewVerifier(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return v.(*jwtVerifier), nil
}

func (v *jwtVerifier) VerifySubject(ctx context.Context, token string) (Subject, error) {
	payload, err := v.verifiedPayload(ctx, token)
	if err != nil {
		return Subject{}, err
	}
	var c struct {
		Iss string          `json:"iss"`
		Aud json.RawMessage `json:"aud"`
		Sub string          `json:"sub"`
		Exp int64           `json:"exp"`
		Nbf int64           `json:"nbf"`
	}
	if err = json.Unmarshal(payload, &c); err != nil {
		return Subject{}, err
	}
	now := time.Now().Unix()
	if c.Iss != v.cfg.IssuerURL || c.Exp <= now || c.Nbf > now || !uuid.MatchString(c.Sub) {
		return Subject{}, errors.New("invalid identity claims")
	}
	var one string
	var many []string
	match := false
	if json.Unmarshal(c.Aud, &one) == nil {
		match = one == v.cfg.Audience
	} else if json.Unmarshal(c.Aud, &many) == nil {
		for _, a := range many {
			match = match || a == v.cfg.Audience
		}
	}
	if !match {
		return Subject{}, errors.New("invalid audience")
	}
	if err = ctx.Err(); err != nil {
		return Subject{}, err
	}
	return Subject{UserID: strings.ToLower(c.Sub), ExpiresAt: time.Unix(c.Exp, 0)}, nil
}
