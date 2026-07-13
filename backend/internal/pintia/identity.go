package pintia

import "regexp"

const maximumIDBytes = 256

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// ValidID reports whether value belongs to the authoritative
// ascendany.pintia.snapshot.v2 $defs.pintiaId domain.
func ValidID(value string) bool {
	return len(value) <= maximumIDBytes && idPattern.MatchString(value)
}
