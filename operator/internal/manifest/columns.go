// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package manifest

import (
	"encoding/json"
	"errors"
	"slices"
)

// AllColumns is the access-rule shorthand for every column the operation may
// use. It is stored as the one-element list ["*"] and expanded by ExpandColumns.
const AllColumns = "*"

// Columns is the column list of an access rule. A manifest may write the
// string "*" in place of the list.
type Columns []string

// UnmarshalJSON accepts a list of names or the string "*".
func (c *Columns) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		if s != AllColumns {
			return errors.New(`columns must be a list of column names or "*"`)
		}
		*c = Columns{AllColumns}
		return nil
	}
	return json.Unmarshal(b, (*[]string)(c))
}

func (c Columns) all() bool { return len(c) == 1 && c[0] == AllColumns }

// ExpandColumns replaces every columns: "*" with the concrete list, so the
// planner, Hasura and core.apps only ever see plain lists. select gets every
// column, system columns included; insert and update get the business
// columns, minus an insert rule's preset columns. A column added later is
// therefore covered by "*" rules on the next upgrade.
func ExpandColumns(app *DXApplication) {
	for i := range app.Spec.Tables {
		t := &app.Spec.Tables[i]
		system := SystemColumns(app, t)
		// The order the sample manifests write: identity first, bookkeeping last.
		lead := []string{"id"}
		if t.IsChild() {
			lead = append(lead, t.BelongsTo.Column)
		}
		lead = slices.DeleteFunc(append(lead, "created_by", "org_unit_id"), func(c string) bool { return system[c] == "" })
		tail := slices.DeleteFunc([]string{"status", "version", "created_at", "updated_at"}, func(c string) bool { return system[c] == "" })
		expand := func(rules []AccessRule, withSystem bool) {
			for j := range rules {
				r := &rules[j]
				if !r.Columns.all() {
					continue
				}
				cols := Columns{}
				if withSystem {
					cols = append(cols, lead...)
				}
				for _, c := range t.Columns {
					if _, preset := r.Presets[c.Name]; !preset {
						cols = append(cols, c.Name)
					}
				}
				if withSystem {
					cols = append(cols, tail...)
				}
				r.Columns = cols
			}
		}
		expand(t.Access.Select, true)
		expand(t.Access.Insert, false)
		expand(t.Access.Update, false)
	}
}
