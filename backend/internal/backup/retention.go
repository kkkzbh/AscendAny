package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type retainedBundle struct {
	id        string
	createdAt time.Time
}

func applyRetention(root string, retainDaily, retainWeekly int, alwaysKeep string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return errors.New("list backup root for retention")
	}
	bundles := make([]retainedBundle, 0)
	for _, entry := range entries {
		if !bundleIDPattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect retained backup %s", entry.Name())
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != backupBundleMode {
			return fmt.Errorf("retained backup %s is not a real %04o directory", entry.Name(), backupBundleMode)
		}
		manifest, _, err := loadManifest(filepath.Join(root, entry.Name()), entry.Name())
		if err != nil {
			return fmt.Errorf("retained backup %s failed manifest verification: %w", entry.Name(), err)
		}
		bundles = append(bundles, retainedBundle{id: entry.Name(), createdAt: manifest.CreatedAt})
	}
	sort.Slice(bundles, func(left, right int) bool {
		if bundles[left].createdAt.Equal(bundles[right].createdAt) {
			return bundles[left].id > bundles[right].id
		}
		return bundles[left].createdAt.After(bundles[right].createdAt)
	})
	keep := map[string]struct{}{alwaysKeep: {}}
	days := make(map[string]struct{})
	weeks := make(map[string]struct{})
	for _, bundle := range bundles {
		day := bundle.createdAt.UTC().Format("2006-01-02")
		if len(days) < retainDaily {
			if _, exists := days[day]; !exists {
				days[day] = struct{}{}
				keep[bundle.id] = struct{}{}
			}
		}
		year, week := bundle.createdAt.UTC().ISOWeek()
		weekKey := fmt.Sprintf("%04d-W%02d", year, week)
		if len(weeks) < retainWeekly {
			if _, exists := weeks[weekKey]; !exists {
				weeks[weekKey] = struct{}{}
				keep[bundle.id] = struct{}{}
			}
		}
	}
	for _, bundle := range bundles {
		if _, retained := keep[bundle.id]; retained {
			continue
		}
		if err := secureRemoveAll(filepath.Join(root, bundle.id), root); err != nil {
			return fmt.Errorf("remove expired backup %s: %w", bundle.id, err)
		}
	}
	if err := syncDirectory(root); err != nil {
		return errors.New("sync backup root after retention")
	}
	return nil
}
