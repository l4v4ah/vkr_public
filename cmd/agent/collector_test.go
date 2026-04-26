package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// buildCollector создаёт минимальный systemCollector для unit-тестов.
func buildCollector() *systemCollector {
	return &systemCollector{
		serviceName:    "test-agent",
		log:            zap.NewNop(),
		alert:          newAlerter("", "", zap.NewNop()),
		sessionID:      "test-session-id",
		thresh:         newThresholds(),
		intervalStatus: statusOK,
	}
}

// ── raiseStatus / popStatus ────────────────────────────────────────────────────

func TestRaiseStatus_WarnFromOK(t *testing.T) {
	c := buildCollector()
	c.raiseStatus(statusWarn)
	assert.Equal(t, statusWarn, c.intervalStatus)
}

func TestRaiseStatus_ErrorFromOK(t *testing.T) {
	c := buildCollector()
	c.raiseStatus(statusError)
	assert.Equal(t, statusError, c.intervalStatus)
}

func TestRaiseStatus_ErrorFromWarn(t *testing.T) {
	c := buildCollector()
	c.raiseStatus(statusWarn)
	c.raiseStatus(statusError)
	assert.Equal(t, statusError, c.intervalStatus)
}

func TestRaiseStatus_WarnDoesNotOverrideError(t *testing.T) {
	c := buildCollector()
	c.raiseStatus(statusError)
	c.raiseStatus(statusWarn) // не должно понизить до warn
	assert.Equal(t, statusError, c.intervalStatus)
}

func TestRaiseStatus_OKNeverRaised(t *testing.T) {
	c := buildCollector()
	c.raiseStatus(statusWarn)
	c.raiseStatus(statusOK) // ok не должен применяться через raiseStatus
	assert.Equal(t, statusWarn, c.intervalStatus, "ok не должен понижать статус")
}

func TestPopStatus_ResetsToOK(t *testing.T) {
	c := buildCollector()
	c.raiseStatus(statusWarn)

	got := c.popStatus()
	assert.Equal(t, statusWarn, got)
	assert.Equal(t, statusOK, c.intervalStatus, "после pop статус должен сброситься в ok")
}

func TestPopStatus_DefaultOK(t *testing.T) {
	c := buildCollector()
	got := c.popStatus()
	assert.Equal(t, statusOK, got)
	assert.Equal(t, statusOK, c.intervalStatus)
}

func TestPopStatus_ErrorResetsToOK(t *testing.T) {
	c := buildCollector()
	c.raiseStatus(statusError)
	got := c.popStatus()
	assert.Equal(t, statusError, got)
	assert.Equal(t, statusOK, c.intervalStatus)
}

// Несколько последовательных pop должны возвращать ok, пока не поднимут статус снова.
func TestPopStatus_SequentialCalls(t *testing.T) {
	c := buildCollector()

	c.raiseStatus(statusWarn)
	assert.Equal(t, statusWarn, c.popStatus())
	assert.Equal(t, statusOK, c.popStatus()) // после сброса — ok
}

// ── thresholds ─────────────────────────────────────────────────────────────────

func TestThresholds_Defaults(t *testing.T) {
	thr := newThresholds()
	assert.Equal(t, 85.0, thr.CPU())
	assert.Equal(t, 90.0, thr.Mem())
	assert.Equal(t, 90.0, thr.Disk())
}

func TestThresholds_UpdateConcurrently(t *testing.T) {
	thr := newThresholds()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			thr.mu.Lock()
			thr.cpu = float64(i)
			thr.mu.Unlock()
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_ = thr.CPU()
	}
	<-done
	// если race detector не обнаружил гонок — тест прошёл
}

// ── alerter.enabled ───────────────────────────────────────────────────────────

func TestAlerterEnabled_BothSet(t *testing.T) {
	a := newAlerter("token", "12345", zap.NewNop())
	assert.True(t, a.enabled())
}

func TestAlerterEnabled_TokenOnly(t *testing.T) {
	a := newAlerter("token", "", zap.NewNop())
	assert.False(t, a.enabled())
}

func TestAlerterEnabled_ChatIDOnly(t *testing.T) {
	a := newAlerter("", "12345", zap.NewNop())
	assert.False(t, a.enabled())
}

func TestAlerterEnabled_NeitherSet(t *testing.T) {
	a := newAlerter("", "", zap.NewNop())
	assert.False(t, a.enabled())
}

// ── newID ─────────────────────────────────────────────────────────────────────

func TestNewID_Length(t *testing.T) {
	id := newID()
	assert.Len(t, id, 32, "ID должен быть 32 hex-символа (16 байт)")
}

func TestNewID_Unique(t *testing.T) {
	seen := make(map[string]struct{}, 200)
	for i := 0; i < 200; i++ {
		id := newID()
		_, exists := seen[id]
		assert.False(t, exists, "каждый ID должен быть уникальным")
		seen[id] = struct{}{}
	}
}
