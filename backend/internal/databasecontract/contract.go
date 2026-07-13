// Package databasecontract verifies the release-bound PostgreSQL schema and
// privilege closure used by backup creation and restore publication.
package databasecontract

//go:generate go run ./cmd/generate

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Profile string

const (
	SourceSnapshot Profile = "source_snapshot"
	Restore        Profile = "restore"
)

type Executor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func Verify(ctx context.Context, executor Executor, profile Profile) error {
	switch profile {
	case SourceSnapshot, Restore:
	default:
		return fmt.Errorf("unknown database verification profile %q", profile)
	}
	if _, err := executor.Exec(
		ctx,
		`SELECT set_config('ascendany.verification_profile', $1, true)`,
		string(profile),
	); err != nil {
		return fmt.Errorf("bind database verification profile: %w", err)
	}
	if _, err := executor.Exec(ctx, verifierSQL); err != nil {
		return fmt.Errorf("database ownership and ACL contract rejected: %w", err)
	}

	expected, err := expectedInventory()
	if err != nil {
		return fmt.Errorf("derive release database object inventory: %w", err)
	}
	rows, err := executor.Query(ctx, inventorySQL)
	if err != nil {
		return fmt.Errorf("read database object inventory: %w", err)
	}
	defer rows.Close()
	actual := make([]string, 0, len(expected))
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return fmt.Errorf("scan database object inventory: %w", err)
		}
		actual = append(actual, key)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read database object inventory: %w", err)
	}
	if err := compareInventory(expected, actual); err != nil {
		return err
	}
	return nil
}

func compareInventory(expected, actual []string) error {
	want := append([]string(nil), expected...)
	got := append([]string(nil), actual...)
	sort.Strings(want)
	sort.Strings(got)
	missing, extra := firstInventoryDifference(want, got)
	if missing == "" && extra == "" {
		return nil
	}
	return fmt.Errorf(
		"database object inventory differs from release contract (expected=%d actual=%d missing=%q extra=%q)",
		len(want), len(got), missing, extra,
	)
}

func firstInventoryDifference(expected, actual []string) (string, string) {
	left, right := 0, 0
	for left < len(expected) && right < len(actual) {
		switch {
		case expected[left] == actual[right]:
			left++
			right++
		case expected[left] < actual[right]:
			return expected[left], ""
		default:
			return "", actual[right]
		}
	}
	if left < len(expected) {
		return expected[left], ""
	}
	if right < len(actual) {
		return "", actual[right]
	}
	return "", ""
}

var errDuplicateInventoryKey = errors.New("duplicate database object inventory key")
