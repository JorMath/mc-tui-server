// scheduler_test.go: regression tests for the scheduled backup clock and the
// daily restart milestones, extracted as pure predicates so the timing rules
// can be tested without a running server.
package main

import (
	"testing"
	"time"
)

func TestBackupDue(t *testing.T) {
	now := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		last      time.Time
		started   bool
		hours     int
		wantFire  bool
		wantAfter time.Time
	}{
		{
			name:      "fresh anchor does not fire",
			last:      time.Time{},
			started:   false,
			hours:     6,
			wantFire:  false,
			wantAfter: now,
		},
		{
			name:      "fresh anchor with hours = 0 still waits for the anchor",
			last:      time.Time{},
			started:   false,
			hours:     0,
			wantFire:  false,
			wantAfter: now,
		},
		{
			name:      "just under the interval",
			last:      now.Add(-(6*time.Hour - time.Minute)),
			started:   true,
			hours:     6,
			wantFire:  false,
			wantAfter: now.Add(-(6*time.Hour - time.Minute)),
		},
		{
			name:      "exactly at the interval",
			last:      now.Add(-6 * time.Hour),
			started:   true,
			hours:     6,
			wantFire:  true,
			wantAfter: now,
		},
		{
			name:      "well past the interval",
			last:      now.Add(-30 * time.Hour),
			started:   true,
			hours:     6,
			wantFire:  true,
			wantAfter: now,
		},
		{
			name:      "hours = 0 fires on the next tick",
			last:      now.Add(-time.Second),
			started:   true,
			hours:     0,
			wantFire:  true,
			wantAfter: now,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fire, anchor := backupDue(tc.last, tc.started, now, tc.hours)
			if fire != tc.wantFire {
				t.Fatalf("backupDue() fire = %v, want %v", fire, tc.wantFire)
			}
			if !anchor.Equal(tc.wantAfter) {
				t.Fatalf("backupDue() anchor = %v, want %v", anchor, tc.wantAfter)
			}
		})
	}
}

func TestRestartEvent(t *testing.T) {
	cases := []struct {
		name    string
		clock   string
		restart string
		want    string
	}{
		{name: "five minute warning", clock: "05:55", restart: "06:00", want: "w5"},
		{name: "one minute warning", clock: "05:59", restart: "06:00", want: "w1"},
		{name: "restart time", clock: "06:00", restart: "06:00", want: "restart"},
		{name: "before the first window", clock: "05:54", restart: "06:00", want: ""},
		{name: "after the restart", clock: "06:01", restart: "06:00", want: ""},
		{name: "no restart configured", clock: "06:00", restart: "", want: ""},
		{name: "invalid restart time", clock: "06:00", restart: "25:00", want: ""},
		{name: "midnight wrap five minute warning", clock: "23:58", restart: "00:03", want: "w5"},
		{name: "midnight wrap one minute warning", clock: "00:02", restart: "00:03", want: "w1"},
		{name: "midnight wrap restart", clock: "00:03", restart: "00:03", want: "restart"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := restartEvent(tc.clock, tc.restart); got != tc.want {
				t.Fatalf("restartEvent(%q, %q) = %q, want %q",
					tc.clock, tc.restart, got, tc.want)
			}
		})
	}
}
