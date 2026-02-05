package model

import (
	"time"
)

type QueryLog struct {
	ID       uint      `gorm:"primarykey" json:"id"`
	ClientIP string    `gorm:"index;size:64" json:"client_ip"`
	QName    string    `gorm:"index" json:"q_name"`
	QType    int       `gorm:"index" json:"q_type"`
	RCode    int       `gorm:"index" json:"r_code"`
	Elapsed  int64     `gorm:"index" json:"elapsed"`
	Time     time.Time `gorm:"index" json:"time"`
}
