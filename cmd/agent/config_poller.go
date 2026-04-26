package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// thresholds holds dynamically configurable warn thresholds.
type thresholds struct {
	mu   sync.RWMutex
	cpu  float64
	mem  float64
	disk float64
}

func newThresholds() *thresholds {
	return &thresholds{cpu: 85, mem: 90, disk: 90}
}

func (t *thresholds) CPU() float64  { t.mu.RLock(); defer t.mu.RUnlock(); return t.cpu }
func (t *thresholds) Mem() float64  { t.mu.RLock(); defer t.mu.RUnlock(); return t.mem }
func (t *thresholds) Disk() float64 { t.mu.RLock(); defer t.mu.RUnlock(); return t.disk }

// pollConfig polls the API for threshold config every 30 s and applies changes.
// Does nothing if apiURL is empty.
func pollConfig(ctx context.Context, apiURL string, t *thresholds) {
	if apiURL == "" {
		return
	}
	url := apiURL + "/api/v1/config/thresholds"

	fetch := func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return
		}
		var cfg struct {
			CPUWarn  float64 `json:"cpu_warn"`
			MemWarn  float64 `json:"mem_warn"`
			DiskWarn float64 `json:"disk_warn"`
		}
		if json.NewDecoder(resp.Body).Decode(&cfg) != nil {
			return
		}
		if cfg.CPUWarn > 0 && cfg.MemWarn > 0 && cfg.DiskWarn > 0 {
			t.mu.Lock()
			t.cpu, t.mem, t.disk = cfg.CPUWarn, cfg.MemWarn, cfg.DiskWarn
			t.mu.Unlock()
		}
	}

	fetch() // apply immediately on start

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fetch()
		}
	}
}
