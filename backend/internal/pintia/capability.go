package pintia

import "regexp"

const ExporterName = "ascendany-pintia-exporter"

var strictSemVerPattern = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`,
)

// SupportsRegistrationNicknameIdentity reports whether exporter provenance
// explicitly guarantees that participant.displayName is PTA user.nickname.
func SupportsRegistrationNicknameIdentity(exporterName, exporterVersion string) bool {
	if exporterName != ExporterName {
		return false
	}
	match := strictSemVerPattern.FindStringSubmatch(exporterVersion)
	if match == nil || match[1] != "2" {
		return false
	}
	minorComparison := compareCanonicalDecimal(match[2], "2")
	if minorComparison > 0 {
		return true
	}
	if minorComparison < 0 {
		return false
	}
	patchComparison := compareCanonicalDecimal(match[3], "3")
	return patchComparison > 0 || patchComparison == 0 && match[4] == ""
}

func compareCanonicalDecimal(left, right string) int {
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
