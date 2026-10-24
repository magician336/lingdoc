package types

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrUnsupportedJSONColumnValue is returned when a value read from a JSON
// column arrives in a shape that no supported driver produces.
//
// It replaces the silent drop that used to happen: a scanner that ignores an
// unrecognised value leaves its receiver zero-valued, and a zero-valued
// configuration is indistinguishable from a configuration nobody set.
var ErrUnsupportedJSONColumnValue = errors.New("unsupported JSON column value type")

// jsonColumnBytes returns the JSON bytes carried by a value read from a JSON
// column.
//
// The Go type a driver hands back is not a property of the column but of the
// dialect and of the storage class of the value that was actually written:
//
//   - PostgreSQL (pgx through database/sql): a json/jsonb column arrives as
//     []byte — pgx's database/sql layer decodes JSONOID and JSONBOID into a
//     byte slice — while a text column arrives as string.
//   - SQLite: the storage class of the value decides. A value stored as TEXT
//     arrives as string, a value stored as BLOB arrives as []byte.
//
// One column therefore has two legitimate carriers, and which one a row
// carries depends on who wrote it: this application binds the []byte that
// json.Marshal returns, while a literal SQL INSERT, an import script or an
// operator's one-off command binds text. Accepting both is what makes a row
// readable regardless of its author.
//
// Any other type is an error rather than a silent zero value. Dropping the
// value would leave the caller with a configuration that looks "not
// configured" — a state that hides the row instead of reporting it.
func jsonColumnBytes(value any) ([]byte, error) {
	switch v := value.(type) {
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedJSONColumnValue, value)
	}
}

// scanJSONColumn reads a value from a JSON column into dst.
//
// A NULL column leaves dst untouched. Each type keeps its own NULL contract
// (a default value, an empty collection) where it declares one; this helper
// only owns the part that every JSON column shares: turning the database
// value into the JSON it holds.
func scanJSONColumn(value any, dst any) error {
	if value == nil {
		return nil
	}
	raw, err := jsonColumnBytes(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}
