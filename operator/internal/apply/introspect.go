// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package apply

import (
	"context"
	"fmt"

	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

// liveColumns returns the columns of a domain table, or nil when it does not exist.
func (r *Runner) liveColumns(ctx context.Context, table string) ([]planner.LiveColumn, error) {
	ref := planner.ParseTableRef(table)
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull
		  FROM pg_attribute a
		  JOIN pg_class c ON c.oid = a.attrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind = 'r'
		   AND a.attnum > 0 AND NOT a.attisdropped
		 ORDER BY a.attnum`, ref.Schema, ref.Name)
	if err != nil {
		return nil, fmt.Errorf("introspect %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	var cols []planner.LiveColumn
	for rows.Next() {
		var c planner.LiveColumn
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}
