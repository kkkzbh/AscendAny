package runtimeapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
)

type artifactReconciler struct {
	store    *artifact.Store
	database interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}
	minAge time.Duration
}

func (reconciler *artifactReconciler) Reconcile(ctx context.Context) error {
	if _, err := reconciler.store.ReconcileIncoming(ctx, reconciler.minAge); err != nil {
		return fmt.Errorf("reconcile incoming artifacts: %w", err)
	}
	_, err := reconciler.store.ReconcilePublished(ctx, reconciler.minAge, reconciler.referenced)
	if err != nil {
		return fmt.Errorf("reconcile published artifacts: %w", err)
	}
	return nil
}

func (reconciler *artifactReconciler) referenced(ctx context.Context, candidate artifact.Artifact) (bool, error) {
	var size int64
	var storageKey string
	err := reconciler.database.QueryRow(ctx, `
SELECT size_bytes, storage_key
FROM ascendany.artifacts
WHERE sha256 = $1`, candidate.Hash).Scan(&size, &storageKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query artifact reference: %w", err)
	}
	if size != candidate.Size || storageKey != candidate.StorageKey {
		return false, fmt.Errorf("database metadata differs for referenced artifact %s", candidate.Hash)
	}
	return true, nil
}
