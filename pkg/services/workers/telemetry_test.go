package workers

import (
	"reflect"
	"testing"
	"time"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func TestUpdateBatteryRejectsOutOfRangeNarrowFields(t *testing.T) {
	w := &TelemetryWorker{}
	state := &shared.EntityState{
		Power: &shared.PowerState{
			BatteryRemain: 42,
			Consumed:      7,
			Cells:         []uint16{12000, 12001},
		},
	}

	w.updateBattery(state, map[string]any{
		"battery_remaining": 200.0,
		"current_consumed":  float64(maxInt32) + 1,
		"voltages":          []any{12000.0, 70000.0},
	}, time.Now())

	if state.Power.BatteryRemain != 42 {
		t.Fatalf("battery remaining wrapped/changed: got %d", state.Power.BatteryRemain)
	}
	if state.Power.Consumed != 7 {
		t.Fatalf("current consumed wrapped/changed: got %d", state.Power.Consumed)
	}
	if want := []uint16{12000, 12001}; !reflect.DeepEqual(state.Power.Cells, want) {
		t.Fatalf("cells changed on invalid voltage: got %v want %v", state.Power.Cells, want)
	}
}

func TestUpdateBatteryAcceptsMAVLinkUnknownRemaining(t *testing.T) {
	w := &TelemetryWorker{}
	state := &shared.EntityState{Power: &shared.PowerState{BatteryRemain: 42}}

	w.updateBattery(state, map[string]any{
		"battery_remaining": -1.0,
	}, time.Now())

	if state.Power.BatteryRemain != -1 {
		t.Fatalf("battery remaining: got %d want -1", state.Power.BatteryRemain)
	}
}
