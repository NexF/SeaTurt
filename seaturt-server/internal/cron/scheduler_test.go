package cron

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCronExpr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr    string
		wantErr bool
	}{
		{"0 9 * * *", false},
		{"*/5 * * * *", false},
		{"0 9 * * 1", false},
		{"0 0 1 * *", false},
		{"bad expr", true},
		{"", true},
		{"0 9 * * * *", true}, // 6 fields (not supported in 5-field mode)
	}

	for _, tt := range tests {
		err := ValidateCronExpr(tt.expr)
		if tt.wantErr {
			assert.Error(t, err, "expr=%q should fail", tt.expr)
		} else {
			assert.NoError(t, err, "expr=%q should pass", tt.expr)
		}
	}
}

func TestNextRunTimes(t *testing.T) {
	t.Parallel()

	times, err := NextRunTimes("0 9 * * *", 5)
	require.NoError(t, err)
	require.Len(t, times, 5)

	// Each time should be at 09:00
	for _, tm := range times {
		assert.Equal(t, 9, tm.Hour())
		assert.Equal(t, 0, tm.Minute())
	}

	// Times should be in ascending order
	for i := 1; i < len(times); i++ {
		assert.True(t, times[i].After(times[i-1]))
	}
}

func TestNextRunTimes_Invalid(t *testing.T) {
	t.Parallel()

	_, err := NextRunTimes("bad", 5)
	assert.Error(t, err)
}

func TestNextRunTimes_EveryFiveMinutes(t *testing.T) {
	t.Parallel()

	times, err := NextRunTimes("*/5 * * * *", 3)
	require.NoError(t, err)
	require.Len(t, times, 3)

	// Each subsequent time should be ~5 minutes apart
	for i := 1; i < len(times); i++ {
		diff := times[i].Sub(times[i-1])
		assert.Equal(t, 5*time.Minute, diff)
	}
}
