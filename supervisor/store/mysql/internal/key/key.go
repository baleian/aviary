// Package key stores the IDs of records in MySQL as 16 bytes.
package key

import (
	"database/sql/driver"

	"github.com/google/uuid"
)

// ID is the ID of a record. Unlike a uuid.UUID, which is written as text, it
// is written to the database as its bytes.
type ID uuid.UUID

func (id ID) Value() (driver.Value, error) {
	return id[:], nil
}

func (id *ID) Scan(source any) error {
	return (*uuid.UUID)(id).Scan(source)
}
