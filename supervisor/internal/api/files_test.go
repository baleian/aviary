package api

import "testing"

// What a runner says of the range that it sends is passed on to the client
// only in the form of such a header.
func TestTheRangeThatARunnerNamesHasItsForm(t *testing.T) {
	for said, part := range map[string]bool{
		"bytes 2-5/12": true, "bytes 0-0/1": true,
		"bytes */12": false, "bytes 2-5/*": false, "items 2-5/12": false,
		"bytes 2-5/12, 7-8/12": false, " bytes 2-5/12": false, "": false,
	} {
		if got := partOfFile.MatchString(said); got != part {
			t.Errorf("%q names a part of a file: %t, want %t", said, got, part)
		}
	}
	for said, none := range map[string]bool{"bytes */12": true, "bytes */0": true, "bytes */": false, "bytes */12 a": false, "bytes 2-5/12": false} {
		if got := noPartOfFile.MatchString(said); got != none {
			t.Errorf("%q names a file without the part: %t, want %t", said, got, none)
		}
	}
}
