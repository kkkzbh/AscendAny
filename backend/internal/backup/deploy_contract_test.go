package backup

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestBackupUnitOwnsVolatilePGPassRuntime(t *testing.T) {
	t.Parallel()
	unit := readBackupContractFile(t, "../../../deploy/v2/systemd/ascendany-backup.service")
	for _, required := range []string{
		"Environment=ASCENDANY_BACKUP_RUNTIME_ROOT=/run/ascendany-backup",
		"RuntimeDirectory=ascendany-backup",
		"RuntimeDirectoryMode=0700",
		"RuntimeDirectoryPreserve=no",
		"ReadWritePaths=/var/backups/ascendany /run/ascendany-backup",
	} {
		if strings.Count(unit, required) != 1 {
			t.Fatalf("backup unit must contain exactly one %q", required)
		}
	}
	backupEnvironment := readBackupContractFile(t, "../../../deploy/v2/config/backup.env.example")
	if hasEnvironmentAssignment(backupEnvironment, "ASCENDANY_BACKUP_RUNTIME_ROOT") ||
		!strings.Contains(backupEnvironment, "ASCENDANY_BACKUP_RUNTIME_ROOT=/run/ascendany-backup") {
		t.Fatal("backup runtime root must be documented but owned only by the systemd unit")
	}
	restoreEnvironment := readBackupContractFile(t, "../../../deploy/v2/config/restore.env.example")
	if hasEnvironmentAssignment(restoreEnvironment, "ASCENDANY_RESTORE_RUNTIME_ROOT") ||
		!strings.Contains(
			restoreEnvironment,
			"ASCENDANY_RESTORE_RUNTIME_ROOT=/run/ascendany-restore-verify-%i",
		) {
		t.Fatal("restore runtime root must be documented for per-instance systemd injection")
	}
	createSource := readBackupContractFile(t, "create.go")
	restoreSource := readBackupContractFile(t, "restore.go")
	if !strings.Contains(
		createSource,
		"pgpassPath := filepath.Join(config.RuntimeRoot, backupPGPassFilename)",
	) || !strings.Contains(
		restoreSource,
		"pgpassPath := filepath.Join(config.RuntimeRoot, restorePGPassFilename)",
	) {
		t.Fatal("database command credentials must be rooted only in the explicit volatile runtime roots")
	}
	for _, forbidden := range []string{".restore-pgpass-", `filepath.Join(stagingPath, ".pgpass")`} {
		if strings.Contains(createSource, forbidden) || strings.Contains(restoreSource, forbidden) {
			t.Fatalf("durable backup or artifact staging still contains pgpass path %q", forbidden)
		}
	}
}

func hasEnvironmentAssignment(contents, name string) bool {
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") && strings.HasPrefix(line, name+"=") {
			return true
		}
	}
	return false
}

func readBackupContractFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
