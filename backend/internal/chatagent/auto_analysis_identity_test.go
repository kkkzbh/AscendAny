package chatagent

import "testing"

func TestAutoAnalysisIdentityDefaultsRoleAndRequiresCanonicalExamUUID(t *testing.T) {
	t.Parallel()
	identity, err := NewAutoAnalysisIdentity("99999999-9999-4999-8999-999999999999", "")
	if err != nil || identity.RoleID != DefaultAutoAnalysisRoleID {
		t.Fatalf("identity=%#v error=%v", identity, err)
	}
	for _, examID := range []string{"", "exam-9", "99999999-9999-5999-8999-999999999999"} {
		if _, err := NewAutoAnalysisIdentity(examID, "role-7"); err == nil {
			t.Fatalf("exam ID %q was accepted", examID)
		}
	}
}
