package cmdtree

// The symbols in this file are for general utility, and may turn out to be
// useful outside this library, therefore they are exported. In a future 2.0
// version they should probably be moved to a separate sub package of this
// library, so that they can be used without including the code for the command
// tree stuff. For now they reside here, so that the library can be vendored
// directly by copying the entire directory.

import (
	"fmt"
	"strings"
)

// SliceShift takes a pointer to a slice of arbitrary type, and shaves off its
// first element, and returning that element, as well as an error
func SliceShift[T any](s *[]T) (T, error) {
	if len(*s) == 0 {
		var ret T
		return ret, fmt.Errorf("empty slice cannot be shifted")
	}
	ret := (*s)[0]
	*s = (*s)[1:]
	return ret, nil
}

// SpaceSplitAndClean splits a string by space characters and returns the
// resluting slice stripped of empty strings.
func SpaceSplitAndClean(s string) []string {
	a := strings.SplitSeq(s, " ")
	final := []string{}
	for p := range a {
		if p == "" {
			continue
		}
		final = append(final, p)
	}
	return final
}
