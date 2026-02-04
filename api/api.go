package api

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"mosdns-log/model"
)

type Handler struct {
	db             *gorm.DB

	statsCache     gin.H
	statsCacheTime time.Time
	statsMutex     sync.Mutex
}

func NewHandler(db *gorm.DB) *Handler {
	return &Handler{
		db:       db,

	}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	api := r.Group("/api")
	
	// Serve static files
	r.Static("/assets", "./web")
	r.StaticFile("/", "./web/index.html")
	r.Static("/css", "./web/css")
	r.Static("/js", "./web/js")

	{
		api.GET("/stats", h.GetStats)
		api.GET("/logs", h.GetLogs)
		api.GET("/clients", h.GetClients)
		api.GET("/qtypes", h.GetQTypes)
		api.GET("/rcodes", h.GetRCodes)

	}
}

func (h *Handler) GetClients(c *gin.Context) {
	var clients []string
	// Get all unique client IPs
	h.db.Model(&model.QueryLog{}).
		Distinct("client_ip").
		Pluck("client_ip", &clients)
	c.JSON(http.StatusOK, clients)
}

func (h *Handler) GetQTypes(c *gin.Context) {
	var types []int
	h.db.Model(&model.QueryLog{}).
		Distinct("q_type").
		Order("q_type").
		Pluck("q_type", &types)
	c.JSON(http.StatusOK, types)
}

func (h *Handler) GetRCodes(c *gin.Context) {
	var rcodes []int
	h.db.Model(&model.QueryLog{}).
		Distinct("r_code").
		Order("r_code").
		Pluck("r_code", &rcodes)
	c.JSON(http.StatusOK, rcodes)
}



func (h *Handler) GetStats(c *gin.Context) {
	h.statsMutex.Lock()
	defer h.statsMutex.Unlock()

	if time.Since(h.statsCacheTime) < 60*time.Second && h.statsCache != nil {
		c.JSON(http.StatusOK, h.statsCache)
		return
	}

	now := time.Now()
	oneDayAgo := now.Add(-24 * time.Hour)
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)

	getLatency := func(since time.Time, minLatencyMicros int64) float64 {
		var avg sql.NullFloat64
		q := h.db.Model(&model.QueryLog{}).
			Select("AVG(elapsed)").
			Where("time > ?", since)
			
		if minLatencyMicros > 0 {
			q = q.Where("elapsed >= ?", minLatencyMicros)
		}
		
		err := q.Row().Scan(&avg)
		if err != nil {
			return 0
		}
		
		if avg.Valid {
			return avg.Float64 / 1000.0
		}
		return 0
	}

	result := gin.H{
		"avg_latency_1d":          getLatency(oneDayAgo, 0),
		"avg_latency_7d":          getLatency(sevenDaysAgo, 0),
		"upstream_avg_latency_1d": getLatency(oneDayAgo, 1000),
		"upstream_avg_latency_7d": getLatency(sevenDaysAgo, 1000),
	}

	h.statsCache = result
	h.statsCacheTime = time.Now()

	c.JSON(http.StatusOK, result)
}

func (h *Handler) GetLogs(c *gin.Context) {
	var logs []model.QueryLog
	
	// Pagination
	page := 1
	if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 0 {
		page = p
	}
	
	// Page Size (Default 50, Max 500)
	pageSize := 50
	if ps, err := strconv.Atoi(c.Query("page_size")); err == nil && ps > 0 {
		if ps > 500 {
			pageSize = 500
		} else {
			pageSize = ps
		}
	}
	
	// Filter
	query := h.db.Model(&model.QueryLog{})
	
	// 1. Type Filter
	if t := c.Query("type"); t != "" {
		query = query.Where("q_type = ?", t)
	}

	// 2. Search (Domain or IP)
	if q := c.Query("search"); q != "" {
		query = query.Where("q_name LIKE ? OR client_ip LIKE ?", "%"+q+"%", "%"+q+"%")
	}
	
	// 3. Exact Client IP Filter
	if ip := c.Query("client_ip"); ip != "" {
		query = query.Where("client_ip = ?", ip)
	}

	// 4. RCode Filter
	if rc := c.Query("r_code"); rc != "" {
		query = query.Where("r_code = ?", rc)
	}

	// 5. Time Range
	if start := c.Query("start_time"); start != "" {
		if t, err := time.Parse(time.RFC3339, start); err == nil {
			query = query.Where("datetime(time) >= datetime(?)", t)
		}
	}
	if end := c.Query("end_time"); end != "" {
		if t, err := time.Parse(time.RFC3339, end); err == nil {
			query = query.Where("datetime(time) <= datetime(?)", t)
		}
	}

	// 6. Sorting
	sort := c.Query("sort")
	switch sort {
	case "latency_desc":
		query = query.Order("elapsed desc")
	case "latency_asc":
		query = query.Order("elapsed asc")
	case "time_asc":
		query = query.Order("time asc")
	default:
		query = query.Order("time desc") // Default
	}

	// Count Total
	var total int64
	query.Count(&total)
	
	// Fetch Page
	result := query.Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&logs)
		
	if result.Error != nil {
		slog.Error("Error fetching logs", "error", result.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
		
	c.JSON(http.StatusOK, gin.H{
		"logs":  logs,
		"total": total,
		"page":  page,
		"page_size": pageSize,
	})
}
