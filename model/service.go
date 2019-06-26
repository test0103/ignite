package model

import (
	"errors"
	"time"

	"github.com/jinzhu/gorm"

	"github.com/go-ignite/ignite-agent/protos"
)

var (
	ErrServiceExists = errors.New("model: user already has a service on this node")
)

type ServiceConfig struct {
	EncryptionMethod protos.ServiceEncryptionMethod_Enum `json:"encryption_method,omitempty"`
}

type Service struct {
	ID              int64 `gorm:"primary_key"`
	UserID          string
	NodeID          string
	ContainerID     string
	Type            protos.ServiceType_Enum
	Port            int
	Config          *ServiceConfig
	LastStatsResult uint64     // last time stats result,unit: byte
	LastStatsTime   *time.Time // last time stats time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time `sql:"index"`

	Password string `gorm:"-"`
}

func NewService(userID, nodeID string, ty protos.ServiceType_Enum, sc *ServiceConfig) *Service {
	return &Service{
		UserID: userID,
		NodeID: nodeID,
		Type:   ty,
		Config: sc,
	}
}

func (h *Handler) GetServices() ([]*Service, error) {
	var services []*Service
	return services, h.db.Find(&services).Error
}

func (h *Handler) GetService(id, userID int64) (*Service, error) {
	r := h.db.Where("id = ?", id)
	if userID > 0 {
		r = r.Where("user_id = ?", userID)
	}

	service := new(Service)
	r = r.First(service)
	if r.RecordNotFound() {
		return nil, nil
	}
	if r.Error != nil {
		return nil, r.Error
	}

	return service, nil
}

func (h *Handler) GetNodePortRangeServiceCount(nodeID string, portFrom, portTo int) (int, error) {
	var count int
	return count, h.db.Model(new(Service)).Where("node_id = ? AND (port < ? OR port > ?)", nodeID, portFrom, portTo).Count(&count).Error
}

func (h *Handler) CreateService(s *Service, f func() error) error {
	return h.runTX(func(tx *gorm.DB) error {
		th := newHandler(tx)
		u, err := th.mustGetUserByID(s.UserID)
		if err != nil {
			return err
		}

		// check if the user create service repeatedly
		count, err := th.checkService(s.UserID, s.NodeID)
		if err != nil {
			return err
		}

		if count > 0 {
			return ErrServiceExists
		}

		s.Password = u.ServicePassword

		// create container so that we can get the port
		if err := f(); err != nil {
			return err
		}

		// TODO there is a problem, create container success but commit transaction error, we need to clean it up
		return tx.Create(s).Error
	})
}

func (h *Handler) checkService(userID, nodeID string) (int, error) {
	var count int
	if err := h.db.Model(Service{}).Where("user_id = ? AND node_id = ?", userID, nodeID).Count(&count).Error; err != nil {
		return 0, err
	}

	return count, nil

}

func (h *Handler) GetServicesByUserID(userID int64) ([]*Service, error) {
	var services []*Service
	return services, h.db.Find(&services, "user_id = ?", userID).Error
}
