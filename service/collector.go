	package service

	import (
		"context"
		"log/slog"
		"strings"
		"sync"
		"time"

		json "github.com/goccy/go-json"
		
		"github.com/hpcloud/tail"
		"gorm.io/gorm"
		"mosdns-log/model"
	)

	const (
		BatchSize = 100
		HeaderScanLimit = 100
	)

	// LogPayload 定义保持不变
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

	type Collector struct {
		db          *gorm.DB
		logPath     string
		ctx         context.Context
		cancel      context.CancelFunc
		wg          sync.WaitGroup
		batchChan   chan []*model.QueryLog
		payloadPool sync.Pool
	}

	func NewCollector(db *gorm.DB, logPath string) *Collector {
		ctx, cancel := context.WithCancel(context.Background())
		return &Collector{
			db:        db,
			logPath:   logPath,
			ctx:       ctx,
			cancel:    cancel,
			batchChan: make(chan []*model.QueryLog, 50),
			payloadPool: sync.Pool{
				New: func() interface{} { return &LogPayload{} },
			},
		}
	}

	func (c *Collector) Start() {
		c.wg.Add(2)
		go c.dbWorker()
		go c.tailWorker()
	}

	func (c *Collector) Stop() {
		c.cancel()
		c.wg.Wait()
	}

func (c *Collector) dbWorker() {
	defer c.wg.Done()

	const sqlHeader = "INSERT INTO query_logs (client_ip, q_name, q_type, r_code, elapsed, time) VALUES "

	for batch := range c.batchChan {
		c.execRawInsert(sqlHeader, batch)
	}
}

func (c *Collector) execRawInsert(sqlHeader string, logs []*model.QueryLog) {
	if len(logs) == 0 {
		return
	}

	valArgs := make([]interface{}, 0, len(logs)*6)
	placeholders := make([]string, 0, len(logs))

	for _, l := range logs {
		placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?)")
		valArgs = append(valArgs,
			l.ClientIP, l.QName,
			l.QType, l.RCode, l.Elapsed, l.Time,
		)
	}

	var sb strings.Builder
	sb.WriteString(sqlHeader)
	sb.WriteString(strings.Join(placeholders, ","))

	err := c.db.WithContext(c.ctx).Exec(sb.String(), valArgs...).Error
	if err != nil {
		slog.Error("[DB] Insert failed", "error", err)
	}
}

	func (c *Collector) tailWorker() {
		defer c.wg.Done()
		defer close(c.batchChan)

		config := tail.Config{
			Location:  &tail.SeekInfo{Offset: 0, Whence: 0},
			ReOpen:    true,
			MustExist: false,
			Poll:      true,
			Follow:    true,
			Logger:    tail.DiscardingLogger,
		}

		t, err := tail.TailFile(c.logPath, config)
		if err != nil {
			slog.Error("Error accessing log file", "path", c.logPath, "error", err)
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
				select {
				case c.batchChan <- buffer:
				case <-time.After(500 * time.Millisecond):
					slog.Warn("Shutdown: Buffer dropped due to full channel")
				}
				buffer = nil
			}
		}

	for {
		select {
		case <-c.ctx.Done():
			t.Stop()
			t.Cleanup()
			sendBuffer()
			return

		case line, ok := <-t.Lines:
			if !ok {
				sendBuffer()
				return
			}
			if line == nil || line.Err != nil {
				continue
			}

			if ql := c.parseLine(line.Text); ql != nil {
				buffer = append(buffer, ql)
				if len(buffer) >= BatchSize {
					sendBuffer()
				}
			}

		case <-ticker.C:
			sendBuffer()
		}
	}
}
	func (c *Collector) parseLine(text string) *model.QueryLog {
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

		if err := json.Unmarshal([]byte(text[idx:]), p); err != nil {
			return nil
		}

		dur, _ := time.ParseDuration(p.Elapsed)
		qname := strings.Clone(strings.TrimSuffix(p.QName, "."))

		return &model.QueryLog{
			ClientIP:   strings.Clone(p.Client),
			QName:      qname,
			QType:      p.QType,
			RCode:      p.RespRCode,
			Elapsed:    dur.Microseconds(),
			Time:       c.parseTime(text),
		}
	}

	func (c *Collector) parseTime(line string) time.Time {
		idx := strings.IndexAny(line, "\t ")
		if idx > 0 {
			t, err := time.Parse("2006-01-02T15:04:05.000-0700", line[:idx])
			if err == nil {
				return t
			}
		}
		return time.Now()
	}