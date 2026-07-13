// Package releaseidentity validates the immutable application identity shared
// by release build metadata, catalog publication intent, and model activation.
package releaseidentity

import (
	"errors"
	"regexp"
	"time"
)

var (
	canonicalSemVer = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|([0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))(\.((0|[1-9][0-9]*)|([0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	lowercaseCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Validate accepts exactly the production release identity emitted by the
// closed release builder.
func Validate(version, commit, buildTime string) error {
	if len(version) > 128 || !canonicalSemVer.MatchString(version) {
		return errors.New("application version must be canonical SemVer")
	}
	if !lowercaseCommit.MatchString(commit) {
		return errors.New("application commit must be 40 lowercase hexadecimal characters")
	}
	if len(buildTime) > 128 {
		return errors.New("application build time must be canonical UTC RFC3339Nano")
	}
	parsed, err := time.Parse(time.RFC3339Nano, buildTime)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != buildTime {
		return errors.New("application build time must be canonical UTC RFC3339Nano")
	}
	return nil
}
