package pintia

import "regexp"

const maximumIDBytes = 256

// ParticipantIdentityAdvisoryLockID serializes reads of current participant
// identity with logical-exam head publication inside PostgreSQL transactions.
const ParticipantIdentityAdvisoryLockID int64 = 0x41534350494e5449

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// ValidID reports whether value belongs to the authoritative
// ascendany.pintia.snapshot.v2 $defs.pintiaId domain.
func ValidID(value string) bool {
	return len(value) <= maximumIDBytes && idPattern.MatchString(value)
}
