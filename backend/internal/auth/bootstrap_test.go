package auth

import (
	"bytes"
	"context"
	"testing"
	"time"
)

type bootstrapMemoryRepository struct {
	result  AdminBootstrapResult
	command AdminBootstrapCommand
}

func (repository *bootstrapMemoryRepository) BootstrapFirstAdmin(
	_ context.Context,
	command AdminBootstrapCommand,
) (AdminBootstrapResult, error) {
	repository.command = command
	if repository.result.Status == AdminBootstrapAlreadyExists {
		return repository.result, nil
	}
	return AdminBootstrapResult{Status: AdminBootstrapCreated, Account: command.Account}, nil
}

func TestAdminBootstrapCreatesAdminWithoutActorIdentity(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	repository := &bootstrapMemoryRepository{}
	random := bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	service, err := NewAdminBootstrapService(repository, AdminBootstrapConfig{
		PasswordPepper: []byte("0123456789abcdef0123456789abcdef"),
		Clock:          fixedClock{now: now},
		Random:         random,
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := service.BootstrapFirstAdmin(context.Background(), AdminBootstrapInput{
		Username:    "admin_1",
		Password:    "long-enough-password",
		DisplayName: "  Administrator  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.Role != RoleAdmin || account.StudentNumber != nil || account.DisplayName != "Administrator" {
		t.Fatalf("admin account = %#v", account)
	}
	if repository.command.Account.StudentNumber != nil || repository.command.Account.Role != RoleAdmin ||
		repository.command.Account.PasswordPHC == "" || !repository.command.Now.Equal(now) {
		t.Fatalf("bootstrap command = %#v", repository.command)
	}
}

func TestAdminBootstrapRejectsSecondAdministrator(t *testing.T) {
	repository := &bootstrapMemoryRepository{result: AdminBootstrapResult{Status: AdminBootstrapAlreadyExists}}
	service, err := NewAdminBootstrapService(repository, AdminBootstrapConfig{
		PasswordPepper: []byte("0123456789abcdef0123456789abcdef"),
		Clock:          fixedClock{now: time.Now()},
		Random:         bytes.NewReader(bytes.Repeat([]byte{0x31}, 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.BootstrapFirstAdmin(context.Background(), AdminBootstrapInput{
		Username:    "admin_1",
		Password:    "long-enough-password",
		DisplayName: "Administrator",
	})
	if ErrorCodeOf(err) != ErrorAdminAlreadyExists {
		t.Fatalf("error = %v", err)
	}
}
