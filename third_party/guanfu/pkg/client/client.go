package client

import (
	"context"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// DataProvider 定义数据获取接口
type DataProvider interface {
	// GetSnapshot 获取指定日期的市场快照
	// 如果是当天，则获取实时数据；如果是历史日期，则获取历史数据
	GetSnapshot(ctx context.Context) (*model.MarketSnapshot, error)
}
