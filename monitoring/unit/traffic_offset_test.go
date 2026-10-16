package monitoring

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadTrafficOffset(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "offset")
	if err := os.WriteFile(p, []byte("up=10\ndown=20\nreset_day=13\nsince=2026-08-13\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	off, ok := readTrafficOffset(p)
	if !ok || off.up != 10 || off.down != 20 || off.resetDay != 13 || off.sinceRaw != "2026-08-13" || off.since.Format("2006-01-02") != "2026-08-13" {
		t.Fatalf("got %+v ok=%v", off, ok)
	}
	_, ok = readTrafficOffset(filepath.Join(dir, "missing"))
	if ok {
		t.Fatal("missing file should not be ok")
	}
}

func TestTrafficOffsetCycleExpires(t *testing.T) {
	dir := t.TempDir()
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, loc)

	// 1) Active cycle offset is applied
	activeFile := filepath.Join(dir, "active")
	if err := os.WriteFile(activeFile, []byte("# comment\nup=100\ndown=200\nreset_day=13\nsince=2026-10-13\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	up, down := applyTrafficOffsetAt(50, 60, 0, activeFile, now)
	if up != 150 || down != 260 {
		t.Fatalf("active cycle: got (%d, %d), want (150, 260)", up, down)
	}

	// Cross-timezone boundary: process local TZ (UTC-8) vs now in UTC+8 at 2026-10-13 00:30:00 +0800
	oldLocal := time.Local
	time.Local = time.FixedZone("UTC-8", -8*3600)
	t.Cleanup(func() { time.Local = oldLocal })
	tzBoundaryNow := time.Date(2026, 10, 13, 0, 30, 0, 0, time.FixedZone("UTC+8", 8*3600))
	up, down = applyTrafficOffsetAt(50, 60, 0, activeFile, tzBoundaryNow)
	if up != 150 || down != 260 {
		t.Fatalf("cross-timezone boundary active cycle: got (%d, %d), want (150, 260)", up, down)
	}

	// 2) Expired cycle offset (since before last reset date 2026-10-13) is ignored
	expiredFile := filepath.Join(dir, "expired")
	if err := os.WriteFile(expiredFile, []byte("up=100\ndown=200\nreset_day=13\nsince=2026-09-13\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	up, down = applyTrafficOffsetAt(50, 60, 0, expiredFile, now)
	if up != 50 || down != 60 {
		t.Fatalf("expired cycle: got (%d, %d), want (50, 60)", up, down)
	}

	// Missing since when MonthRotate is active is ignored; permanent offset when resetDay==0 works without since
	noSinceFile := filepath.Join(dir, "no_since")
	if err := os.WriteFile(noSinceFile, []byte("up=100\ndown=200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	up, down = applyTrafficOffsetAt(50, 60, 13, noSinceFile, now)
	if up != 50 || down != 60 {
		t.Fatalf("missing since with MonthRotate=13: got (%d, %d), want (50, 60)", up, down)
	}
	up, down = applyTrafficOffsetAt(50, 60, 0, noSinceFile, now)
	if up != 150 || down != 260 {
		t.Fatalf("permanent offset with resetDay=0: got (%d, %d), want (150, 260)", up, down)
	}

	// 3) Near uint64 max saturates to math.MaxUint64 without wraparound
	satFile := filepath.Join(dir, "saturate")
	satContent := fmt.Sprintf("up=%d\ndown=100\nreset_day=13\nsince=2026-10-13\n", uint64(math.MaxUint64-10))
	if err := os.WriteFile(satFile, []byte(satContent), 0o644); err != nil {
		t.Fatal(err)
	}
	up, down = applyTrafficOffsetAt(50, math.MaxUint64-20, 0, satFile, now)
	if up != math.MaxUint64 || down != math.MaxUint64 {
		t.Fatalf("saturation: got (%d, %d), want (%d, %d)", up, down, uint64(math.MaxUint64), uint64(math.MaxUint64))
	}

	// 4) Corrupted offset files are rejected and not applied
	corruptCases := []struct {
		name    string
		content string
	}{
		{"no_equals", "up=100\ndown=200\ncorrupted_line\n"},
		{"unknown_key", "up=100\ndown=200\nfoo=bar\n"},
		{"bad_up", "up=notanumber\ndown=200\n"},
		{"bad_reset_day_high", "up=100\ndown=200\nreset_day=32\nsince=2026-10-13\n"},
		{"bad_reset_day_zero", "up=100\ndown=200\nreset_day=0\nsince=2026-10-13\n"},
		{"bad_since", "up=100\ndown=200\nreset_day=13\nsince=2026-13-45\n"},
		{"reset_day_without_since", "up=100\ndown=200\nreset_day=13\n"},
	}
	for _, tc := range corruptCases {
		p := filepath.Join(dir, "corrupt_"+tc.name)
		if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
			t.Fatal(err)
		}
		gotUp, gotDown := applyTrafficOffsetAt(50, 60, 0, p, now)
		if gotUp != 50 || gotDown != 60 {
			t.Fatalf("corrupt case %s: got (%d, %d), want (50, 60)", tc.name, gotUp, gotDown)
		}
	}
}
