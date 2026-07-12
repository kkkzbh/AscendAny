package auth

import (
	"context"
	"crypto/rand"
	"io"
	"time"
)

// AdminBootstrapInput contains the human-owned values for the one-time first
// administrator transaction. The password remains process-local and is never
// written to logs or command-line arguments by the provided CLI.
type AdminBootstrapInput struct {
	Username    string
	Password    string
	DisplayName string
}

type AdminBootstrapCommand struct {
	Account AccountRecord
	Now     time.Time
}

type AdminBootstrapStatus uint8

const (
	AdminBootstrapCreated AdminBootstrapStatus = iota + 1
	AdminBootstrapAlreadyExists
)

type AdminBootstrapResult struct {
	Status  AdminBootstrapStatus
	Account AccountRecord
}

type AdminBootstrapRepository interface {
	BootstrapFirstAdmin(context.Context, AdminBootstrapCommand) (AdminBootstrapResult, error)
}

type AdminBootstrapConfig struct {
	PasswordPepper []byte
	Clock          Clock
	Random         io.Reader
}

type AdminBootstrapService struct {
	repository AdminBootstrapRepository
	passwords  *PasswordHasher
	clock      Clock
	random     io.Reader
}

func NewAdminBootstrapService(
	repository AdminBootstrapRepository,
	config AdminBootstrapConfig,
) (*AdminBootstrapService, error) {
	if repository == nil {
		return nil, authError(ErrorInvalidConfiguration, "Admin bootstrap repository is required.", nil)
	}
	if config.Clock == nil {
		return nil, authError(ErrorInvalidConfiguration, "Admin bootstrap clock is required.", nil)
	}
	if config.Random == nil {
		return nil, authError(ErrorInvalidConfiguration, "Admin bootstrap random source is required.", nil)
	}
	passwords, err := NewPasswordHasher(config.PasswordPepper, config.Random)
	if err != nil {
		return nil, err
	}
	return &AdminBootstrapService{
		repository: repository,
		passwords:  passwords,
		clock:      config.Clock,
		random:     config.Random,
	}, nil
}

func ProductionAdminBootstrapConfig(passwordPepper []byte) AdminBootstrapConfig {
	return AdminBootstrapConfig{
		PasswordPepper: passwordPepper,
		Clock:          systemClock{},
		Random:         rand.Reader,
	}
}

func (s *AdminBootstrapService) BootstrapFirstAdmin(
	ctx context.Context,
	input AdminBootstrapInput,
) (Account, error) {
	if err := validateUsername(input.Username); err != nil {
		return Account{}, err
	}
	if err := validatePassword(input.Password); err != nil {
		return Account{}, err
	}
	displayName, err := validateTrimmedField(
		"Display name",
		input.DisplayName,
		MinDisplayNameBytes,
		MaxDisplayNameBytes,
	)
	if err != nil {
		return Account{}, err
	}
	passwordPHC, err := s.passwords.Hash(input.Password)
	if err != nil {
		return Account{}, err
	}
	accountID, err := newUUIDv4(s.random)
	if err != nil {
		return Account{}, err
	}
	account := AccountRecord{
		Account: Account{
			ID:            accountID,
			Username:      input.Username,
			DisplayName:   displayName,
			StudentNumber: nil,
			Role:          RoleAdmin,
			AuthRevision:  1,
		},
		PasswordPHC: passwordPHC,
	}
	result, err := s.repository.BootstrapFirstAdmin(ctx, AdminBootstrapCommand{
		Account: account,
		Now:     s.clock.Now(),
	})
	if err != nil {
		return Account{}, err
	}
	if result.Status != AdminBootstrapCreated {
		return Account{}, adminAlreadyExists()
	}
	return result.Account.Account, nil
}
