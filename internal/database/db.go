package database

import (
	"context"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var db *gorm.DB
var once sync.Once

func Get() *gorm.DB {
	once.Do(func() {
		_ = godotenv.Load()

		dsn := os.Getenv("DATABASE_URL")
		var err error
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err != nil {
			log.Fatal("Failed to connect to database:", err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			log.Fatal("Failed to configure database pool:", err)
		}
		sqlDB.SetMaxOpenConns(envInt("DB_MAX_OPEN_CONNS", 25))
		sqlDB.SetMaxIdleConns(envInt("DB_MAX_IDLE_CONNS", 10))
		sqlDB.SetConnMaxLifetime(time.Duration(envInt("DB_CONN_MAX_LIFETIME_SECONDS", 1800)) * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(ctx); err != nil {
			log.Fatal("Database ping failed:", err)
		}
	})

	return db
}

func Ping(ctx context.Context) error {
	sqlDB, err := Get().DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

func Close() error {
	sqlDB, err := Get().DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}
