package importing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
)

var lowercaseSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Service struct {
	repository serviceRepository
	uuid       uuidGenerator
}

func newService(repository serviceRepository, uuid uuidGenerator) (*Service, error) {
	if repository == nil {
		return nil, importError(ErrorInvalidConfiguration, false, "construct service", errors.New("repository is required"))
	}
	if uuid == nil {
		return nil, importError(ErrorInvalidConfiguration, false, "construct service", errors.New("UUID generator is required"))
	}
	return &Service{repository: repository, uuid: uuid}, nil
}

// QueuePublication commits the artifact reference, idempotent job, and first
// durable event before releasing the publication's per-hash flock. Release is
// attempted after every commit or rollback outcome.
func (s *Service) QueuePublication(
	ctx context.Context,
	publication *artifact.Publication,
	mediaType string,
) (_ QueueResult, resultErr error) {
	if publication == nil {
		return QueueResult{}, importError(ErrorInvalidPublication, false, "queue publication", errors.New("publication is required"))
	}
	defer func() {
		if err := publication.Release(); err != nil {
			releaseErr := importError(ErrorInvalidPublication, false, "release publication", err)
			if resultErr == nil {
				resultErr = releaseErr
			} else {
				resultErr = errors.Join(resultErr, releaseErr)
			}
		}
	}()
	if ctx == nil {
		return QueueResult{}, importError(ErrorInvalidPublication, false, "queue publication", errors.New("context is required"))
	}
	if mediaType != PintiaSnapshotV2MediaType {
		return QueueResult{}, importError(
			ErrorInvalidMediaType,
			true,
			"queue publication",
			fmt.Errorf("media type must be %q", PintiaSnapshotV2MediaType),
		)
	}
	if err := validatePublishedArtifact(publication.Artifact); err != nil {
		return QueueResult{}, err
	}
	publicID, err := s.uuid()
	if err != nil {
		return QueueResult{}, err
	}
	return s.repository.QueueArtifact(ctx, publication.Artifact, mediaType, publicID)
}

func (s *Service) Claim(ctx context.Context, owner string, leaseDuration time.Duration) (*Claim, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, importError(ErrorInvalidConfiguration, false, "claim job", errors.New("lease owner is required"))
	}
	if leaseDuration <= 0 {
		return nil, importError(ErrorInvalidConfiguration, false, "claim job", errors.New("lease duration must be positive"))
	}
	return s.repository.Claim(ctx, owner, leaseDuration)
}

func validatePublishedArtifact(value artifact.Artifact) error {
	if !lowercaseSHA256Pattern.MatchString(value.Hash) {
		return importError(ErrorInvalidPublication, false, "queue publication", errors.New("artifact hash must be lowercase SHA-256"))
	}
	if value.Size <= 0 {
		return importError(ErrorInvalidPublication, false, "queue publication", errors.New("artifact size must be positive"))
	}
	expectedKey := "sha256/" + value.Hash[:2] + "/" + value.Hash
	if value.StorageKey != expectedKey {
		return importError(ErrorInvalidPublication, false, "queue publication", fmt.Errorf("artifact storage key must be %q", expectedKey))
	}
	if strings.TrimSpace(value.Path) == "" {
		return importError(ErrorInvalidPublication, false, "queue publication", errors.New("artifact path is required"))
	}
	return nil
}
