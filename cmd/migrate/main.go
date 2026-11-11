// Command migrate applies versioned PostgreSQL migrations (golang-migrate + embed.FS).
package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run() error {
	direction := flag.String("direction", "up", "migration direction: up or down")
	force := flag.Int("force", 0, "force a dirty migration version (negative clears dirty); 0 disables")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{})
	if err != nil {
		return fmt.Errorf("driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if *force != 0 {
		if err := m.Force(*force); err != nil {
			return fmt.Errorf("force: %w", err)
		}
	}

	switch *direction {
	case "up":
		err = m.Up()
	case "down":
		err = m.Down()
	default:
		return fmt.Errorf("unknown direction %q", *direction)
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	version, dirty, stateErr := m.Version()
	if stateErr != nil && !errors.Is(stateErr, migrate.ErrNilVersion) {
		return stateErr
	}
	fmt.Printf("migration %s complete: version=%d dirty=%v\n", *direction, version, dirty)
	return nil
}
