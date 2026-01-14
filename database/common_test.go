package database

import (
	"database/sql"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"strings"
	"testing"
)

func sqlmockNew(t *testing.T) (*gorm.DB, *sql.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	dialector := postgres.New(postgres.Config{
		DSN:        "sqlmock_db_0",
		DriverName: "postgres",
		Conn:       db,
	})
	gormdb, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		t.Fatal(err.Error())
	}
	return gormdb, db, mock
}

func assertDiff(t *testing.T, diff DifferentAttr, name string, previous, current interface{}) {
	assert.Equal(t, name, diff.Name())
	assert.Equal(t, previous, diff.Previous())
	assert.Equal(t, current, diff.Current())
}

func upex(s string) string {
	return fmt.Sprintf(`"%s"="excluded"."%s"`, s, s)
}

func upexes(cols []string) string {
	u := make([]string, len(cols))
	for i, col := range cols {
		u[i] = upex(col)
	}
	return strings.Join(u, ",")
}
