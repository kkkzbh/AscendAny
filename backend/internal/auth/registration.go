package auth

import (
	"context"
)

func (s *Service) Register(ctx context.Context, input RegistrationInput) (AuthResult, error) {
	if err := validateAuthContext(ctx); err != nil {
		return AuthResult{}, err
	}
	if err := validateRegistrationInput(input); err != nil {
		return AuthResult{}, err
	}

	release, acquired := s.passwordWork.tryAcquire()
	if !acquired {
		return AuthResult{}, passwordWorkSaturated()
	}
	passwordPHC, hashErr := s.passwords.Hash(input.Password)
	release()
	if hashErr != nil {
		return AuthResult{}, hashErr
	}
	if err := ctx.Err(); err != nil {
		return AuthResult{}, canceled(err)
	}

	accountID, sessionID, credential, csrf, jwtID, err := s.issueSessionCredentials()
	if err != nil {
		return AuthResult{}, err
	}
	now := canonicalAuthTime(s.clock.Now())
	sessionExpiry := now.Add(s.refreshTTL)
	studentNumber := input.StudentNumber
	ptaNickname := input.PTANickname
	command := RegisterStudentCommand{
		Account: AccountRecord{
			Account: Account{
				ID:            accountID,
				Username:      input.Username,
				DisplayName:   input.Username,
				StudentNumber: &studentNumber,
				PTANickname:   &ptaNickname,
				Role:          RoleStudent,
				AuthRevision:  1,
			},
			PasswordPHC: passwordPHC,
		},
		SessionID:     sessionID,
		RefreshToken:  newRefreshToken(credential, csrf, now, sessionExpiry),
		Now:           now,
		SessionExpiry: sessionExpiry,
	}
	result, err := s.repository.RegisterStudent(ctx, command)
	if err != nil {
		return AuthResult{}, err
	}
	switch result.Status {
	case RegistrationUsernameUnavailable:
		return AuthResult{}, registrationUsernameUnavailable()
	case RegistrationIdentityUnavailable:
		return AuthResult{}, registrationIdentityUnavailable()
	case StudentRegistered:
	default:
		return AuthResult{}, authError(ErrorInternal, "Student registration result is invalid.", nil)
	}
	if err := validateAccountRecord(result.Account); err != nil ||
		result.Account.ID != accountID ||
		result.Account.Username != input.Username ||
		result.Account.PasswordPHC != passwordPHC ||
		result.Account.StudentNumber == nil || *result.Account.StudentNumber != input.StudentNumber ||
		result.Account.PTANickname == nil || *result.Account.PTANickname != input.PTANickname ||
		result.Account.Role != RoleStudent || result.AuthenticatedAt.IsZero() {
		return AuthResult{}, authError(ErrorInternal, "Registered student account is invalid.", err)
	}
	return s.authResult(result.Account.Account, sessionID, jwtID, credential, csrf, result.AuthenticatedAt)
}

func validateRegistrationInput(input RegistrationInput) error {
	if err := validateUsername(input.Username); err != nil {
		return err
	}
	if err := validatePassword(input.Password); err != nil {
		return err
	}
	studentNumber, err := validateTrimmedField(
		"Student ID",
		input.StudentNumber,
		MinStudentNumberBytes,
		MaxStudentNumberBytes,
	)
	if err != nil {
		return err
	}
	ptaNickname, err := validateTrimmedField(
		"PTA nickname",
		input.PTANickname,
		MinPTANicknameBytes,
		MaxPTANicknameBytes,
	)
	if err != nil {
		return err
	}
	if studentNumber != input.StudentNumber || ptaNickname != input.PTANickname {
		return authError(ErrorInvalidInput, "Registration identity fields must be canonical trimmed UTF-8 strings.", nil)
	}
	return nil
}
