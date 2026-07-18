package auth

import (
	"context"
	"testing"
	"time"
)

func (r *memoryRepository) RegisterStudent(
	_ context.Context,
	command RegisterStudentCommand,
) (RegisterStudentResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	studentNumber := *command.Account.StudentNumber
	if !r.enrollment.actorNumbers[studentNumber] ||
		r.enrollment.actorNicknames[studentNumber] != *command.Account.PTANickname {
		return RegisterStudentResult{Status: RegistrationIdentityUnavailable}, nil
	}
	for _, account := range r.accountsByID {
		if account.Username == command.Account.Username {
			return RegisterStudentResult{Status: RegistrationUsernameUnavailable}, nil
		}
		if account.StudentNumber != nil && *account.StudentNumber == studentNumber {
			return RegisterStudentResult{Status: RegistrationIdentityUnavailable}, nil
		}
	}
	for _, grant := range r.enrollment.grantsByID {
		if grant.terminal != "" || !grant.grant.ExpiresAt.After(command.Now) {
			continue
		}
		if grant.grant.Username == command.Account.Username {
			return RegisterStudentResult{Status: RegistrationUsernameUnavailable}, nil
		}
		if grant.grant.StudentNumber == studentNumber {
			return RegisterStudentResult{Status: RegistrationIdentityUnavailable}, nil
		}
	}
	account := command.Account
	r.accountsByID[account.ID] = account
	r.accountsByUsername[account.Username] = account
	r.storeSession(
		account.ID,
		account.AuthRevision,
		command.SessionID,
		command.Now,
		command.SessionExpiry,
		command.RefreshToken,
	)
	return RegisterStudentResult{
		Status:          StudentRegistered,
		Account:         account,
		AuthenticatedAt: command.Now,
	}, nil
}

func TestRegisterCreatesBoundStudentAndAuthenticatedSession(t *testing.T) {
	now := time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	repository.enrollment.actorNumbers["20260001"] = true
	repository.enrollment.actorNicknames["20260001"] = "Alice"
	service := newTestService(t, repository, now)

	result, err := service.Register(context.Background(), RegistrationInput{
		Username:      "alice_01",
		Password:      "passw0rd",
		StudentNumber: "20260001",
		PTANickname:   "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Account.Username != "alice_01" || result.Account.StudentNumber == nil ||
		*result.Account.StudentNumber != "20260001" || result.Account.PTANickname == nil ||
		*result.Account.PTANickname != "Alice" || result.Account.Role != RoleStudent ||
		result.AccessToken == "" || result.RefreshCookieValue == "" || result.CSRFToken == "" {
		t.Fatalf("registration result = %#v", result)
	}
	stored := repository.accountsByID[result.Account.ID]
	verified, verifyErr := service.passwords.Verify("passw0rd", stored.PasswordPHC)
	if verifyErr != nil || !verified {
		t.Fatalf("stored password hash did not verify: verified=%v err=%v", verified, verifyErr)
	}
	loggedIn, err := service.Login(context.Background(), LoginInput{
		Username: "alice_01",
		Password: "passw0rd",
	})
	if err != nil || loggedIn.Account != result.Account {
		t.Fatalf("registered login = %#v, err=%v", loggedIn.Account, err)
	}
}

func TestRegisterReturnsDeterministicUsernameAndIdentityConflicts(t *testing.T) {
	now := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	repository.enrollment.actorNumbers["20260001"] = true
	repository.enrollment.actorNicknames["20260001"] = "Alice"
	repository.enrollment.actorNumbers["20260002"] = true
	repository.enrollment.actorNicknames["20260002"] = "Bob"
	service := newTestService(t, repository, now)
	register := func(input RegistrationInput) error {
		_, err := service.Register(context.Background(), input)
		return err
	}
	first := RegistrationInput{
		Username: "alice_01", Password: "long-enough-password",
		StudentNumber: "20260001", PTANickname: "Alice",
	}
	if err := register(first); err != nil {
		t.Fatal(err)
	}
	if code := ErrorCodeOf(register(RegistrationInput{
		Username: "alice_01", Password: "long-enough-password",
		StudentNumber: "20260002", PTANickname: "Bob",
	})); code != ErrorRegistrationUsername {
		t.Fatalf("username conflict code = %q", code)
	}
	if code := ErrorCodeOf(register(RegistrationInput{
		Username: "alice_02", Password: "long-enough-password",
		StudentNumber: "20260001", PTANickname: "Alice",
	})); code != ErrorRegistrationIdentity {
		t.Fatalf("identity conflict code = %q", code)
	}
	if code := ErrorCodeOf(register(RegistrationInput{
		Username: "alice_03", Password: "long-enough-password",
		StudentNumber: "20260002", PTANickname: "Wrong",
	})); code != ErrorRegistrationIdentity {
		t.Fatalf("identity proof failure code = %q", code)
	}
}

func TestRegistrationInputRequiresCanonicalFrontendIdentity(t *testing.T) {
	valid := RegistrationInput{
		Username: "alice_01", Password: "long-enough-password",
		StudentNumber: "20260001", PTANickname: "Alice",
	}
	tests := []struct {
		name   string
		mutate func(*RegistrationInput)
	}{
		{name: "username", mutate: func(input *RegistrationInput) { input.Username = "Alice" }},
		{name: "password", mutate: func(input *RegistrationInput) { input.Password = "short" }},
		{name: "student whitespace", mutate: func(input *RegistrationInput) { input.StudentNumber = " 20260001" }},
		{name: "nickname whitespace", mutate: func(input *RegistrationInput) { input.PTANickname = "Alice " }},
		{name: "nickname missing", mutate: func(input *RegistrationInput) { input.PTANickname = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			if code := ErrorCodeOf(validateRegistrationInput(input)); code != ErrorInvalidInput {
				t.Fatalf("validation code = %q", code)
			}
		})
	}
}
