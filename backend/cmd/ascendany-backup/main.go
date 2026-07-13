package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kkkzbh/AscendAny/backend/internal/backup"
	"github.com/kkkzbh/AscendAny/backend/internal/version"
)

type createFunc func(context.Context, backup.CreateConfig) (backup.CreateResult, error)
type verifyFunc func(context.Context, backup.VerifyConfig, string) (backup.VerifyResult, error)
type restoreFunc func(context.Context, backup.RestoreConfig, string) (backup.RestoreResult, error)

type operations struct {
	create  createFunc
	verify  verifyFunc
	restore restoreFunc
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.LookupEnv, os.ReadFile, os.Stderr, operations{
		create:  backup.Create,
		verify:  backup.Verify,
		restore: backup.RestoreVerify,
	}))
}

func run(
	ctx context.Context,
	args []string,
	lookup backup.LookupEnv,
	readFile backup.ReadFile,
	stderr io.Writer,
	operations operations,
) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	command, backupID, err := parseCommand(args)
	if err != nil {
		logger.Error("command rejected", "error", err)
		return 2
	}
	switch command {
	case "create":
		if operations.create == nil {
			logger.Error("backup creator is required")
			return 1
		}
		configuration, err := backup.LoadCreateConfig(lookup, readFile)
		if err != nil {
			logger.Error("configuration rejected", "error", err)
			return 1
		}
		result, err := operations.create(ctx, configuration)
		if err != nil {
			logger.Error("backup creation failed", "error", err)
			return 1
		}
		logger.Info(
			"backup published",
			"backupId", result.BackupID,
			"manifestSHA256", result.ManifestSHA256,
			"artifactCount", result.ArtifactCount,
		)
		return 0
	case "verify":
		if operations.verify == nil {
			logger.Error("backup verifier is required")
			return 1
		}
		configuration, err := backup.LoadVerifyConfig(lookup)
		if err != nil {
			logger.Error("configuration rejected", "error", err)
			return 1
		}
		result, err := operations.verify(ctx, configuration, backupID)
		if err != nil {
			logger.Error("backup verification failed", "error", err)
			return 1
		}
		logger.Info(
			"backup verified",
			"backupId", result.BackupID,
			"manifestSHA256", result.ManifestSHA256,
			"artifactCount", result.ArtifactCount,
		)
		return 0
	case "restore-verify":
		if operations.restore == nil {
			logger.Error("restore verifier is required")
			return 1
		}
		configuration, err := backup.LoadRestoreConfig(lookup, readFile)
		if err != nil {
			logger.Error("configuration rejected", "error", err)
			return 1
		}
		result, err := operations.restore(ctx, configuration, backupID)
		if err != nil {
			logger.Error("backup restore verification failed", "error", err)
			return 1
		}
		release := version.Current()
		logger.Info(
			"backup restore verified",
			"backupId", result.BackupID,
			"manifestSHA256", result.ManifestSHA256,
			"artifactCount", result.ArtifactCount,
			"databaseName", result.DatabaseName,
			"releaseCommit", release.Commit,
			"releaseVersion", release.Version,
			"modelId", result.RecommendationModel.ModelID,
			"modelPurpose", result.RecommendationModel.ModelPurpose,
			"modelArtifactSHA256", result.RecommendationModel.ArtifactSHA256,
			"modelHeadRevision", result.RecommendationModel.HeadRevision,
			"modelApplicationVersion", result.RecommendationModel.ApplicationVersion,
			"modelApplicationCommit", result.RecommendationModel.ApplicationCommit,
			"modelApplicationBuildTime", result.RecommendationModel.ApplicationBuildTime,
			"modelFeatureSchemaSHA256", result.RecommendationModel.FeatureSchemaSHA256,
			"modelKnowledgeCatalogSHA256", result.RecommendationModel.KnowledgeCatalogSHA256,
			"modelManifestSHA256", result.RecommendationModel.ManifestSHA256,
		)
		return 0
	default:
		logger.Error("unreachable backup command")
		return 1
	}
}

func parseCommand(args []string) (string, string, error) {
	if len(args) == 1 && args[0] == "create" {
		return "create", "", nil
	}
	if len(args) == 2 && (args[0] == "verify" || args[0] == "restore-verify") {
		if err := validateCLIBackupID(args[1]); err != nil {
			return "", "", err
		}
		return args[0], args[1], nil
	}
	return "", "", errors.New("usage: ascendany-backup create | verify BACKUP_ID | restore-verify BACKUP_ID")
}

func validateCLIBackupID(value string) error {
	if len(value) != len("backup-20060102T150405Z-0000000000000000") {
		return errors.New("backup id is invalid")
	}
	// The package performs canonical validation before filesystem access. This
	// check keeps malformed CLI input out of configuration loading and logs.
	for index, character := range value {
		switch {
		case index < 7 && character == rune("backup-"[index]):
		case index >= 7 && index <= 14 && character >= '0' && character <= '9':
		case index == 15 && character == 'T':
		case index >= 16 && index <= 21 && character >= '0' && character <= '9':
		case index == 22 && character == 'Z':
		case index == 23 && character == '-':
		case index >= 24 && character >= '0' && character <= '9':
		case index >= 24 && character >= 'a' && character <= 'f':
		default:
			return errors.New("backup id is invalid")
		}
	}
	return nil
}
