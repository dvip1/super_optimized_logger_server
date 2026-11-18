package database

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// DB is the shared database handle.
var DB *sql.DB

func Init(path string) error {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("ping sqlite: %w", err)
	}
	DB = db
	return nil
}

// Close closes the database handle if open.
func Close() error {
	if DB == nil {
		return nil
	}
	return DB.Close()
}
