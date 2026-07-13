package recommendation

import (
	"context"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func TestStagedReaderServiceRejectsModelReadsWithoutRepositoryAccess(t *testing.T) {
	service := NewStagedReaderService()

	if _, err := service.ReadCurrent(context.Background(), auth.AccessPrincipal{Role: auth.RoleStudent}); CodeOf(err) != ErrorModelInactive || !IsPermanent(err) {
		t.Fatalf("student staged read returned %v", err)
	}
}

func TestStagedReaderServiceRetainsPrincipalContract(t *testing.T) {
	service := NewStagedReaderService()

	if _, err := service.ReadCurrent(context.Background(), auth.AccessPrincipal{Role: auth.RoleAdmin}); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("student staged role contract returned %v", err)
	}
	var nilService *StagedReaderService
	if _, err := nilService.ReadCurrent(context.Background(), auth.AccessPrincipal{Role: auth.RoleStudent}); CodeOf(err) != ErrorInvalidInput {
		t.Fatalf("nil staged service returned %v", err)
	}
}
