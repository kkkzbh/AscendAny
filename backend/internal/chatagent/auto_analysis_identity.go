package chatagent

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	DefaultAutoAnalysisRoleID  = "xiaoD"
	MaxAutoAnalysisRoleIDBytes = maximumAutoAnalysisRoleIDBytes
)

func NewAutoAnalysisIdentity(examID, roleID string) (AutoAnalysisIdentity, error) {
	if roleID == "" {
		roleID = DefaultAutoAnalysisRoleID
	}
	identity := AutoAnalysisIdentity{ExamID: examID, RoleID: roleID}
	if err := validateAutoAnalysisIdentity(identity); err != nil {
		return AutoAnalysisIdentity{}, err
	}
	return identity, nil
}

func validateAutoAnalysisIdentity(identity AutoAnalysisIdentity) error {
	if !canonicalUUIDv4.MatchString(identity.ExamID) ||
		identity.RoleID == "" || identity.RoleID != strings.TrimSpace(identity.RoleID) ||
		len(identity.RoleID) > MaxAutoAnalysisRoleIDBytes || !utf8.ValidString(identity.RoleID) || strings.ContainsRune(identity.RoleID, '\x00') {
		return errors.New("canonical exam UUID and bounded role identity are required")
	}
	return nil
}
