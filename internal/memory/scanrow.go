package memory

import (
	"database/sql"
	"fmt"
	"reflect"
)

// scanRow reads the current row into a newly allocated T, matching each result
// column to the struct field tagged `db:"<column>"`. database/sql.Rows.Scan has
// no struct support of its own — it insists on exactly one destination per
// column — so the index would otherwise be unreadable on any query that returns
// a whole entry row. This maps by column name rather than position, so adding a
// column to a SELECT (a snippet, a join kind) never silently misaligns a field.
//
// Columns with no matching struct field are scanned into a throwaway, which is
// what lets one Entry row carry an extra `snippet` or `kind` column without
// those polluting the entry model.
func scanRow[T any](rows *sql.Rows) (T, error) {
	var out T
	cols, err := rows.Columns()
	if err != nil {
		return out, fmt.Errorf("read column names: %w", err)
	}

	val := reflect.ValueOf(&out).Elem()
	typ := val.Type()
	fieldByColumn := make(map[string]int, typ.NumField())
	for i := range typ.NumField() {
		f := typ.Field(i)
		tag, ok := f.Tag.Lookup("db")
		if !ok || tag == "" || tag == "-" {
			continue
		}
		fieldByColumn[tag] = i
	}

	dests := make([]any, len(cols))
	for i, col := range cols {
		if idx, ok := fieldByColumn[col]; ok {
			dests[i] = val.Field(idx).Addr().Interface()
			continue
		}
		dests[i] = new(any)
	}

	if err := rows.Scan(dests...); err != nil {
		return out, fmt.Errorf("scan row into %s: %w", typ.String(), err)
	}
	return out, nil
}
