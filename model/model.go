package model

import (
	"time"
)

type QueryLog struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	ClientIP   string    `gorm:"index;size:64" json:"client_ip"`
	Protocol   string    `gorm:"size:10" json:"protocol"`
	ServerName string    `gorm:"size:255" json:"server_name"`
	QName      string    `gorm:"index" json:"q_name"`
	QType      int       `gorm:"index" json:"q_type"`
	QClass     int       `json:"q_class"`
	RCode      int       `gorm:"index" json:"r_code"`
	Elapsed    int64     `gorm:"index" json:"elapsed"`
	Time       time.Time `gorm:"index" json:"time"`
}
