package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestOpenComparisonDatabaseAppliesPragmasToEveryConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generation-1.db")
	writable, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writable.Exec("CREATE TABLE marker (value TEXT); INSERT INTO marker VALUES ('generation-1')"); err != nil {
		t.Fatal(err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}

	const connectionCount = 3
	database, err := openComparisonDatabase(context.Background(), path, connectionCount)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	err = forEachComparisonConnection(context.Background(), database, connectionCount, func(ctx context.Context, connection *sql.Conn) error {
		if err := verifyComparisonConnection(ctx, connection); err != nil {
			return err
		}
		var value string
		if err := connection.QueryRowContext(ctx, "SELECT value FROM marker").Scan(&value); err != nil {
			return err
		}
		if value != "generation-1" {
			return fmt.Errorf("marker = %q, want generation-1", value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := database.Stats().MaxOpenConnections; got != connectionCount {
		t.Fatalf("MaxOpenConnections = %d, want %d", got, connectionCount)
	}
}

func TestOpenComparisonDatabaseRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generation-1.db")
	writable, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writable.Exec("CREATE TABLE marker (value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := openComparisonDatabase(context.Background(), path, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("INSERT INTO marker VALUES ('unexpected')"); err == nil {
		t.Fatal("read-only comparison database accepted a write")
	}
}
