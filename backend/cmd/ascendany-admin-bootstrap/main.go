package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/config"
	"github.com/kkkzbh/AscendAny/backend/internal/database"
	"github.com/kkkzbh/AscendAny/backend/internal/migrate"
)

type command struct {
	username    string
	displayName string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	command, err := parseCommand(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "usage: ascendany-admin-bootstrap create --username <name> --display-name <name>")
		return 2
	}
	password, err := readAdminPasswordCredential(os.Getenv("CREDENTIALS_DIRECTORY"))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "administrator password file was rejected")
		return 1
	}

	configuration, err := config.LoadAdminBootstrap(os.LookupEnv, os.ReadFile)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "runtime configuration was rejected")
		return 1
	}
	if configuration.Database.ExpectedSchemaVersion != migrate.CurrentVersion() {
		_, _ = fmt.Fprintln(stderr, "database schema version configuration was rejected")
		return 1
	}
	pool, err := database.Open(context.Background(), database.PoolOptions{
		URL:                   configuration.Database.URL,
		Password:              configuration.Database.Password,
		MaxConnections:        configuration.Database.MaxConnections,
		MinConnections:        configuration.Database.MinConnections,
		ConnectTimeout:        configuration.Database.ConnectTimeout,
		MaxConnectionLifetime: configuration.Database.MaxConnectionLifetime,
		MaxConnectionIdleTime: configuration.Database.MaxConnectionIdleTime,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "database connection initialization failed")
		return 1
	}
	defer pool.Close()
	manifest := migrate.Manifest()
	expectedMigrations := make([]database.ExpectedMigration, 0, len(manifest))
	for _, entry := range manifest {
		expectedMigrations = append(expectedMigrations, database.ExpectedMigration{
			Version: entry.Version,
			Name:    entry.Name,
			SHA256:  entry.SHA256,
		})
	}
	migrationReader, err := database.NewMigrationStateReader(pool, expectedMigrations)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "database migration verifier initialization failed")
		return 1
	}
	verificationContext, cancelVerification := context.WithTimeout(
		context.Background(),
		configuration.Database.HealthTimeout,
	)
	defer cancelVerification()
	if err := pool.Ping(verificationContext); err != nil {
		_, _ = fmt.Fprintln(stderr, "database readiness verification failed")
		return 1
	}
	migrationState, err := migrationReader.State(verificationContext)
	if err != nil || migrationState.Version != configuration.Database.ExpectedSchemaVersion {
		_, _ = fmt.Fprintln(stderr, "database migration history was rejected")
		return 1
	}

	repository, err := auth.NewPostgresRepository(pool)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "administrator bootstrap repository initialization failed")
		return 1
	}
	service, err := auth.NewAdminBootstrapService(
		repository,
		auth.ProductionAdminBootstrapConfig([]byte(configuration.PasswordPepper)),
	)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "administrator bootstrap service initialization failed")
		return 1
	}
	account, err := service.BootstrapFirstAdmin(context.Background(), auth.AdminBootstrapInput{
		Username:    command.username,
		Password:    password,
		DisplayName: command.displayName,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "administrator bootstrap failed: %s\n", auth.ErrorCodeOf(err))
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(account); err != nil {
		_, _ = fmt.Fprintln(stderr, "administrator bootstrap succeeded but output encoding failed")
		return 1
	}
	return 0
}

func parseCommand(args []string) (command, error) {
	if len(args) == 0 || args[0] != "create" {
		return command{}, errors.New("create command is required")
	}
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var parsed command
	flags.StringVar(&parsed.username, "username", "", "canonical admin username")
	flags.StringVar(&parsed.displayName, "display-name", "", "administrator display name")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return command{}, errors.New("invalid command arguments")
	}
	if parsed.username == "" || parsed.displayName == "" {
		return command{}, errors.New("all command flags are required")
	}
	return parsed, nil
}

func readAdminPasswordCredential(credentialsDirectory string) (string, error) {
	if !filepath.IsAbs(credentialsDirectory) || filepath.Clean(credentialsDirectory) != credentialsDirectory {
		return "", errors.New("credentials directory must be absolute and normalized")
	}
	path := filepath.Join(credentialsDirectory, "admin_password")
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !pathInfo.Mode().IsRegular() {
		return "", errors.New("password credential must be regular")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return "", errors.New("opened password credential does not match the validated file")
	}
	content, err := io.ReadAll(io.LimitReader(file, auth.MaxPasswordBytes+1))
	if err != nil {
		return "", err
	}
	if len(content) > auth.MaxPasswordBytes {
		return "", errors.New("password file exceeds maximum password length")
	}
	return string(content), nil
}
