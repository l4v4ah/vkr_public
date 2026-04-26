package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/slava-kov/monitoring-system/internal/storage"
)

// ThresholdConfig holds warn thresholds for system metrics (percent values).
// Persisted in the settings table so values survive API restarts.
type ThresholdConfig struct {
	CPUWarn  float64 `json:"cpu_warn"`
	MemWarn  float64 `json:"mem_warn"`
	DiskWarn float64 `json:"disk_warn"`
}

type thresholdStore struct {
	mu  sync.RWMutex
	cfg ThresholdConfig
	db  *storage.DB
	log *zap.Logger
}

func newThresholdStore(db *storage.DB, log *zap.Logger) *thresholdStore {
	s := &thresholdStore{
		cfg: ThresholdConfig{CPUWarn: 85, MemWarn: 90, DiskWarn: 90},
		db:  db,
		log: log,
	}
	// Load persisted values from DB; fall back to defaults on error.
	var cfg ThresholdConfig
	if err := db.LoadSettings(context.Background(), storage.KeyThresholds, &cfg); err == nil {
		if cfg.CPUWarn > 0 && cfg.MemWarn > 0 && cfg.DiskWarn > 0 {
			s.cfg = cfg
		}
	}
	return s
}

func (s *thresholdStore) Get() ThresholdConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *thresholdStore) handleGet(c *gin.Context) {
	c.JSON(http.StatusOK, s.Get())
}

func (s *thresholdStore) handleSet(c *gin.Context) {
	var cfg ThresholdConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if cfg.CPUWarn < 1 || cfg.CPUWarn > 100 ||
		cfg.MemWarn < 1 || cfg.MemWarn > 100 ||
		cfg.DiskWarn < 1 || cfg.DiskWarn > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "thresholds must be between 1 and 100"})
		return
	}

	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	if err := s.db.SaveSettings(c.Request.Context(), storage.KeyThresholds, cfg); err != nil {
		s.log.Warn("persist thresholds", zap.Error(err))
	}

	c.JSON(http.StatusOK, s.Get())
}
