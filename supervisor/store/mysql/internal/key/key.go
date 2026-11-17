// Package key is the IDs of the records as MySQL keeps them: in sixteen
// bytes.
package key

import (
	"database/sql/driver"

	"github.com/google/uuid"
)

// ID is the ID of a record. It is a UUID that is written to the database as
// its bytes, and not as the text that a UUID writes itself as.
type ID uuid.UUID

func (id ID) Value() (driver.Value, error) {
	return id[:], nil
}

func (id *ID) Scan(source any) error {
	return (*uuid.UUID)(id).Scan(source)
}
