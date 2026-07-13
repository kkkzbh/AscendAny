package migrate

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var migrationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type Definition struct {
	Version int64
	Name    string
	SHA256  string
	SQL     string
}

type HistoryEntry struct {
	Version int64
	Name    string
	SHA256  string
}

var embeddedManifest = []HistoryEntry{
	{Version: 1, Name: "fresh_schema", SHA256: "0cffdb00acefd37c049a654bad76d8fac79727ed7c54cc3fa9234d54964ce0cf"},
	{Version: 2, Name: "product_domains", SHA256: "1762304608ed3f93d62c01ad494a2b6110b07737cc652f38a2581392985fdd36"},
	{Version: 3, Name: "recommendation_catalog_contract", SHA256: "6fa4a81fbe3440fc4b149a5b77d6c3860031e285bafef50b5a881e8783f36267"},
	{Version: 4, Name: "achievement_rules", SHA256: "3242ddfbdee0911d961ebe0f46237f6e2b8a6e7c5e09cf1d94f6ae98c4caaccb"},
	{Version: 5, Name: "auto_analysis_once", SHA256: "40fed038bc7773f45e940de2880ca18427573e10555937afa202e684aecdaa17"},
	{Version: 6, Name: "inference_model_runtime", SHA256: "330bd7bebdd6e67572a76fcb0c1e84c897df2a766f6e821312c46ecfc18e39ea"},
	{Version: 7, Name: "catalog_publication_provenance", SHA256: "a69c081d1b0eaa31df8490773d3feed355fdb4053925f84087552df9b5fc940b"},
}

func CurrentVersion() int64 {
	return embeddedManifest[len(embeddedManifest)-1].Version
}

// Manifest returns a copy of the immutable migration identity compiled into
// the binary. Runtime readiness uses the same values as the migrator.
func Manifest() []HistoryEntry {
	return append([]HistoryEntry(nil), embeddedManifest...)
}

func Embedded() ([]Definition, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("list embedded migrations: %w", err)
	}
	filenames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("unexpected embedded migration directory %q", entry.Name())
		}
		filenames = append(filenames, entry.Name())
	}
	if err := validateMigrationFilenames(filenames, embeddedManifest); err != nil {
		return nil, err
	}

	definitions := make([]Definition, 0, len(embeddedManifest))
	for _, entry := range embeddedManifest {
		path := fmt.Sprintf("migrations/%04d_%s.sql", entry.Version, entry.Name)
		contents, err := fs.ReadFile(migrationFiles, path)
		if err != nil {
			return nil, fmt.Errorf("read embedded migration %s: %w", path, err)
		}
		digest := sha256.Sum256(contents)
		actual := hex.EncodeToString(digest[:])
		if actual != entry.SHA256 {
			return nil, fmt.Errorf("embedded migration %d (%s) hash drift: got %s, want %s", entry.Version, entry.Name, actual, entry.SHA256)
		}
		definitions = append(definitions, Definition{
			Version: entry.Version,
			Name:    entry.Name,
			SHA256:  entry.SHA256,
			SQL:     string(contents),
		})
	}
	if err := ValidateDefinitions(definitions); err != nil {
		return nil, err
	}
	return definitions, nil
}

func validateMigrationFilenames(filenames []string, manifest []HistoryEntry) error {
	actual := append([]string(nil), filenames...)
	sort.Strings(actual)
	expected := make([]string, 0, len(manifest))
	for _, entry := range manifest {
		expected = append(expected, fmt.Sprintf("%04d_%s.sql", entry.Version, entry.Name))
	}
	sort.Strings(expected)
	if len(actual) != len(expected) {
		return fmt.Errorf("embedded migration file set has %d files, manifest has %d", len(actual), len(expected))
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return fmt.Errorf("unknown embedded migration file %q; expected %q", actual[index], expected[index])
		}
	}
	return nil
}

func ValidateDefinitions(definitions []Definition) error {
	if len(definitions) == 0 {
		return errors.New("at least one embedded migration is required")
	}
	for index, migration := range definitions {
		expectedVersion := int64(index + 1)
		if migration.Version != expectedVersion {
			return fmt.Errorf("embedded migration gap at index %d: got version %d, want %d", index, migration.Version, expectedVersion)
		}
		if !migrationNamePattern.MatchString(migration.Name) {
			return fmt.Errorf("embedded migration %d has invalid name %q", migration.Version, migration.Name)
		}
		if len(migration.SHA256) != sha256.Size*2 {
			return fmt.Errorf("embedded migration %d has invalid SHA-256", migration.Version)
		}
		decoded, err := hex.DecodeString(migration.SHA256)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("embedded migration %d has invalid SHA-256", migration.Version)
		}
		actual := sha256.Sum256([]byte(migration.SQL))
		if hex.EncodeToString(actual[:]) != migration.SHA256 {
			return fmt.Errorf("embedded migration %d (%s) SQL hash drift", migration.Version, migration.Name)
		}
	}
	return nil
}

func ValidateHistory(history []HistoryEntry, definitions []Definition) error {
	if err := ValidateDefinitions(definitions); err != nil {
		return err
	}
	if len(history) > len(definitions) {
		return fmt.Errorf("migration history contains %d entries, binary knows %d", len(history), len(definitions))
	}
	for index, entry := range history {
		expectedVersion := int64(index + 1)
		if entry.Version != expectedVersion {
			return fmt.Errorf("migration history is not a contiguous prefix: got version %d at index %d, want %d", entry.Version, index, expectedVersion)
		}
		expected := definitions[index]
		if entry.Name != expected.Name {
			return fmt.Errorf("migration history name drift at version %d: got %q, want %q", entry.Version, entry.Name, expected.Name)
		}
		if entry.SHA256 != expected.SHA256 {
			return fmt.Errorf("migration history hash drift at version %d", entry.Version)
		}
	}
	return nil
}
