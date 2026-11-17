// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package agent

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge"
)

type ChatAuthenticator struct {
	Verifier auth.SubjectVerifier
	DB       *sql.DB
}

func (a ChatAuthenticator) Authenticate(r *http.Request) (Actor, error) {
	if a.Verifier == nil || a.DB == nil {
		return Actor{}, knowledge.ErrAuthorityUnavailable
	}
	if r == nil || r.Context().Err() != nil {
		return Actor{}, auth.ErrUnauthenticated
	}
	headers := r.Header.Values("Authorization")
	if len(headers) != 1 {
		return Actor{}, auth.ErrUnauthenticated
	}
	fields := strings.Fields(headers[0])
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return Actor{}, auth.ErrUnauthenticated
	}
	subject, err := a.Verifier.VerifySubject(r.Context(), fields[1])
	if err != nil || !validID(subject.UserID) || !subject.ExpiresAt.After(time.Now()) {
		return Actor{}, auth.ErrUnauthenticated
	}
	var active bool
	if err := a.DB.QueryRowContext(r.Context(), `SELECT cap_ai.agent_chat_user_active($1)`, subject.UserID).Scan(&active); err != nil {
		return Actor{}, knowledge.ErrAuthorityUnavailable
	}
	if !active || r.Context().Err() != nil || !subject.ExpiresAt.After(time.Now()) {
		return Actor{}, auth.ErrUnauthenticated
	}
	return Actor{UserID: strings.ToLower(subject.UserID)}, nil
}
