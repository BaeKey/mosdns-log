package service

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	json "github.com/goccy/go-json"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"mosdns-log/config"
	"mosdns-log/model"
)

const (
	BatchSize       = 100
	HeaderScanLimit = 200
	timeLayout      = "2006-01-02T15:04:05.000-0700"
)

// LogPayload 用于解析 JSON 日志行
type LogPayload struct {
	UQID       int    `json:"uqid"`
	Client     string `json:"client"`
	Protocol   string `json:"protocol"`
	ServerName string `json:"server_name"`
	QName      string `json:"qname"`
	QType      int    `json:"qtype"`
	QClass     int    `json:"qclass"`
	RespRCode  int    `json:"resp_rcode"`
	Elapsed    string `json:"elapsed"`
}

func (p *LogPayload) Reset() {
	p.UQID = 0; p.Client = ""; p.Protocol = ""; p.ServerName = ""
	p.QName = ""; p.QType = 0; p.QClass = 0; p.RespRCode = 0; p.Elapsed = ""
}

func stringToBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// ============================================================================
// Collector: 日志采集器
// ============================================================================

type Collector struct {
	db          *gorm.DB
	logPath     string
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	batchChan   chan []*model.QueryLog
	payloadPool sync.Pool
	fileMu      sync.Mutex
}

func NewCollector(db *gorm.DB, logPath string) *Collector {
	// 调整 GORM Logger 以避免插入大量日志时的噪音
	if db.Config.Logger == nil || db.Config.Logger != logger.Discard {
		db.Config.Logger = logger.Default.LogMode(logger.Silent)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Collector{
		db:        db,
		logPath:   logPath,
		ctx:       ctx,
		cancel:    cancel,
		batchChan: make(chan []*model.QueryLog, 200),
		payloadPool: sync.Pool{
			New: func() interface{} { return &LogPayload{} },
		},
	}
}

func (c *Collector) Start() {
	c.wg.Add(2)
	go c.dbWorker()
	go c.tailWorker()
	slog.Info("Collector started")
}

func (c *Collector) Stop() {
	c.cancel()
	c.wg.Wait()
	slog.Info("Collector stopped")
}

// dbWorker 负责批量插入数据库
func (c *Collector) dbWorker() {
	defer c.wg.Done()
	const sqlHeader = "INSERT INTO query_logs (client_ip, q_name, q_type, r_code, elapsed, time) VALUES "
	for batch := range c.batchChan {
		c.execRawInsert(sqlHeader, batch)
	}
}

// execRawInsert 执行原生 SQL 插入以提高性能
func (c *Collector) execRawInsert(sqlHeader string, logs []*model.QueryLog) {
	if len(logs) == 0 {
		return
	}
	valArgs := make([]interface{}, 0, len(logs)*6)
	placeholders := make([]string, 0, len(logs))
	for _, l := range logs {
		placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?)")
		valArgs = append(valArgs, l.ClientIP, l.QName, l.QType, l.RCode, l.Elapsed, l.Time)
	}
	var sb strings.Builder
	sb.WriteString(sqlHeader)
	sb.WriteString(strings.Join(placeholders, ","))

	dbCtx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()

	err := c.db.WithContext(dbCtx).Exec(sb.String(), valArgs...).Error
	if err != nil {
		slog.Error("[DB] Insert failed", "error", err)
	}
}

// tailWorker 负责监听文件变化并解析日志
func (c *Collector) tailWorker() {
	defer c.wg.Done()
	defer close(c.batchChan)

	var (
		file   *os.File
		reader *bufio.Reader
		inode  uint64
		offset int64
		err    error
	)

	defer func() {
		c.fileMu.Lock()
		if file != nil {
			file.Close()
		}
		c.fileMu.Unlock()
	}()

	openFile := func(seekEnd bool) bool {
		c.fileMu.Lock()
		defer c.fileMu.Unlock()

		file, err = os.Open(c.logPath)
		if err != nil {
			slog.Error("Failed to open log file", "path", c.logPath, "error", err)
			return false
		}

		stat, err := file.Stat()
		if err == nil {
			if sys := stat.Sys(); sys != nil {
				if statT, ok := sys.(*syscall.Stat_t); ok {
					inode = statT.Ino
				}
			}
		}

		if seekEnd {
			file.Seek(0, io.SeekEnd)
		} else {
			file.Seek(0, io.SeekStart)
		}

		offset, _ = file.Seek(0, io.SeekCurrent)
		reader = bufio.NewReader(file)
		slog.Info("Log file opened", "path", c.logPath, "offset", offset)
		return true
	}

	// 首次启动时尝试打开文件
	if !openFile(false) {
		return
	}

	buffer := make([]*model.QueryLog, 0, BatchSize)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	sendBuffer := func() {
		if len(buffer) == 0 {
			return
		}
		select {
		case c.batchChan <- buffer:
			buffer = make([]*model.QueryLog, 0, BatchSize)
		case <-c.ctx.Done():
			// 退出时的最后尝试
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case c.batchChan <- buffer:
			case <-timer.C:
			}
			timer.Stop()
			buffer = nil
		}
	}

	for {
		select {
		case <-c.ctx.Done():
			sendBuffer()
			return
		case <-ticker.C:
			sendBuffer()
		default:
		}

		line, err := reader.ReadString('\n')

		if err != nil {
			if err == io.EOF {
				time.Sleep(500 * time.Millisecond)
				sendBuffer() // EOF 时立即发送缓存

				newStat, statErr := os.Stat(c.logPath)
				if statErr != nil {
					continue
				}

				var newInode uint64
				if sys := newStat.Sys(); sys != nil {
					if statT, ok := sys.(*syscall.Stat_t); ok {
						newInode = statT.Ino
					}
				}

				// 检测轮转：Inode 改变（外部轮转）或 大小变小（Truncate）
				if newInode != inode || newStat.Size() < offset {
					slog.Info("Log rotation detected, reopening file...")

					c.fileMu.Lock()
					file.Close()
					file = nil
					c.fileMu.Unlock()

					if openFile(false) {
						continue
					} else {
						return
					}
				}
				continue
			} else {
				slog.Error("Error reading log line", "error", err)
				time.Sleep(1 * time.Second)
				continue
			}
		}

		offset += int64(len(line))
		if ql := c.parseLine(line); ql != nil {
			buffer = append(buffer, ql)
			if len(buffer) >= BatchSize {
				sendBuffer()
			}
		}
	}
}

func (c *Collector) parseLine(text string) *model.QueryLog {
	if len(text) < 50 {
		return nil
	}

	scanLen := len(text)
	if scanLen > HeaderScanLimit {
		scanLen = HeaderScanLimit
	}
	if !strings.Contains(text[:scanLen], "_query_summary") {
		return nil
	}

	idx := strings.Index(text, "{")
	if idx == -1 {
		return nil
	}

	p := c.payloadPool.Get().(*LogPayload)
	defer func() {
		p.Reset()
		c.payloadPool.Put(p)
	}()

	if err := json.Unmarshal(stringToBytes(text[idx:]), p); err != nil {
		return nil
	}

	dur, _ := time.ParseDuration(p.Elapsed)

	return &model.QueryLog{
		ClientIP: p.Client,
		QName:    strings.TrimSuffix(p.QName, "."),
		QType:    p.QType,
		RCode:    p.RespRCode,
		Elapsed:  dur.Microseconds(),
		Time:     c.parseTime(text),
	}
}

func (c *Collector) parseTime(line string) time.Time {
	idx := strings.IndexAny(line, "\t ")
	if idx > 0 {
		t, err := time.Parse(timeLayout, line[:idx])
		if err == nil {
			return t
		}
	}
	return time.Now()
}

// ============================================================================
// Cleaner: 数据库维护与日志轮转
// ============================================================================

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
	slog.Info("Cleaner started")
}

func (c *Cleaner) Stop() {
	c.cancel()
	c.wg.Wait()
	slog.Info("Cleaner stopped")
}

// optimizeDB 将 SQLite 配置为高性能模式
func (c *Cleaner) optimizeDB() {
	// Note: WAL mode is likely already set in DSN
	if err := c.db.Exec("PRAGMA synchronous=NORMAL;").Error; err != nil {
		slog.Error("Failed to set synchronous mode", "error", err)
	}
}

// runVacuum 定期整理数据库碎片
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

// runRetention 定期清理过期数据
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

// runLogRotation 检查日志文件大小并执行 Truncate
func (c *Cleaner) runLogRotation() {
	defer c.wg.Done()
	interval := time.Duration(c.conf.LogCheckIntervalMin) * time.Minute
	if interval <= 0 {
		interval = 60 * time.Minute
	}

	// 转换为字节
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