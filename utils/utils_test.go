package utils

import (
	"testing"
	"time"
)

func TestGetLastResetDateClampMonthEnd(t *testing.T) {
	loc := time.UTC
	tests := []struct {
		name      string
		resetDay  int
		current   time.Time
		wantYear  int
		wantMonth time.Month
		wantDay   int
	}{
		{
			name:     "reset day 31 in February common year clamps to Feb 28",
			resetDay: 31,
			current:  time.Date(2026, 3, 15, 12, 0, 0, 0, loc),
			wantYear: 2026, wantMonth: time.February, wantDay: 28,
		},
		{
			name:     "reset day 31 in February leap year clamps to Feb 29",
			resetDay: 31,
			current:  time.Date(2028, 3, 15, 12, 0, 0, 0, loc),
			wantYear: 2028, wantMonth: time.February, wantDay: 29,
		},
		{
			name:     "reset day 31 in March uses Mar 31 normally",
			resetDay: 31,
			current:  time.Date(2026, 4, 5, 12, 0, 0, 0, loc),
			wantYear: 2026, wantMonth: time.March, wantDay: 31,
		},
		{
			name:     "current after this-month reset day returns this month",
			resetDay: 13,
			current:  time.Date(2026, 10, 15, 12, 0, 0, 0, loc),
			wantYear: 2026, wantMonth: time.October, wantDay: 13,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetLastResetDate(tt.resetDay, tt.current)
			want := time.Date(tt.wantYear, tt.wantMonth, tt.wantDay, 0, 0, 0, 0, loc)
			if !got.Equal(want) {
				t.Fatalf("GetLastResetDate(%d, %v) = %v, want %v", tt.resetDay, tt.current, got, want)
			}
		})
	}
}
