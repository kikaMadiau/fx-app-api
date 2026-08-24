package storage

import (
	"fx-app-api/internal/domain/trader/entity"
)

type TraderStorage interface {
	Init() error
	CreateTrader(trader *entity.Trader) error
	GetTrader(id int) (*entity.Trader, error)
	GetTraderByEmail(email string) (*entity.Trader, error)
	FindOrCreateTrader(trader *entity.Trader) (*entity.Trader, error)
	UpdateTrader(trader *entity.Trader) error
	DeleteTrader(id int) error
}
