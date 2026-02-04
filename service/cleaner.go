package service

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"gorm.io/gorm"
	"mosdns-log/config"
	"mosdns-log/model"
)

type Cleaner struct {
	db     *gorm.DB
	conf   *config.Config
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewCleaner(db *gorm.DB, conf *config.Config) *Cleaner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Cleaner{
		db:     db,
		conf:   conf,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (c *Cleaner) Start() {
	c.optimizeDB()

	c.wg.Add(3)
	go c.runRetention()
	go c.runLogRotation()
	go c.runVacuum()
}

func (c *Cleaner) Stop() {
	c.cancel()
	c.wg.Wait()
	slog.Info("Cleaner stopped")
}

// optimizeDB 将 SQLite 配置为高性能模式
func (c *Cleaner) optimizeDB() {
	// Note: WAL mode is already set in DSN (main.go)
	if err := c.db.Exec("PRAGMA synchronous=NORMAL;").Error; err != nil {
		slog.Error("Failed to set synchronous mode", "error", err)
	}
}

func (c *Cleaner) runVacuum() {
	defer c.wg.Done()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			slog.Info("Running DB Vacuum...")
			if err := c.db.Exec("VACUUM").Error; err != nil {
				slog.Error("Vacuum failed", "error", err)
			}
		}
	}
}

func (c *Cleaner) runRetention() {
	defer c.wg.Done()
	interval := time.Duration(c.conf.DBCheckIntervalMin) * time.Minute
	if interval <= 0 {
		interval = 60 * time.Minute
	}

	doCleanup := func() {
		days := c.conf.DBRetentionDays
		if days <= 0 {
			days = 7
		}
		deadline := time.Now().AddDate(0, 0, -days)

		const batchSize = 1000
		totalDeleted := 0

		for {
			select {
			case <-c.ctx.Done():
				return
			default:
			}

			var ids []uint
			err := c.db.Model(&model.QueryLog{}).
				Where("time < ?", deadline).
				Limit(batchSize).
				Pluck("id", &ids).Error

			if err != nil {
				slog.Error("Retention cleanup query failed", "error", err)
				break
			}

			if len(ids) == 0 {
				break
			}

			if err := c.db.Delete(&model.QueryLog{}, ids).Error; err != nil {
				slog.Error("Retention batch delete failed", "error", err)
				break
			}

			totalDeleted += len(ids)
			time.Sleep(50 * time.Millisecond)
		}

		if totalDeleted > 0 {
			slog.Info("Retention cleanup finished", "deleted_rows", totalDeleted)
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			doCleanup()
		}
	}
}

func (c *Cleaner) runLogRotation() {
	defer c.wg.Done()
	interval := time.Duration(c.conf.LogCheckIntervalMin) * time.Minute
	if interval <= 0 {
		interval = 60 * time.Minute
	}
	
	maxSize := int64(c.conf.LogMaxSizeMB) * 1024 * 1024 
	ticker := time.NewTicker(interval) 
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if c.conf.LogPath == "" {
				continue
			}

			fi, err := os.Stat(c.conf.LogPath)
			if err != nil {
				continue
			}

			if fi.Size() > maxSize {
				slog.Info("Log file size limit reached. Truncating...", 
					"size", fi.Size(), 
					"limit", maxSize)
				
				if err := os.Truncate(c.conf.LogPath, 0); err != nil {
					slog.Error("Failed to truncate log file", "error", err)
				}
			}
		}
	}
}