package monitoring

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/komari-monitor/komari-agent/utils"
)

const trafficOffsetFile = "/opt/komari/traffic-offset"

type trafficOffset struct {
	up, down uint64
	resetDay int
	since    time.Time
	sinceRaw string
}

func addUint64Saturating(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

// applyTrafficOffset adds this billing-cycle baseline. After the next
// reset day the file is ignored so totals start from the new cycle.
func applyTrafficOffset(up, down uint64) (uint64, uint64) {
	return applyTrafficOffsetAt(up, down, flags.MonthRotate, trafficOffsetFile, time.Now())
}

func applyTrafficOffsetAt(up, down uint64, resetDay int, path string, now time.Time) (uint64, uint64) {
	off, ok := readTrafficOffset(path)
	if !ok {
		return up, down
	}
	effectiveResetDay := off.resetDay
	if effectiveResetDay == 0 {
		effectiveResetDay = resetDay
	}
	if effectiveResetDay < 0 || effectiveResetDay > 31 {
		return up, down
	}
	if effectiveResetDay >= 1 && effectiveResetDay <= 31 {
		if off.sinceRaw == "" {
			return up, down
		}
		loc := now.Location()
		last := utils.GetLastResetDate(effectiveResetDay, now)
		lastResetDate, err1 := time.ParseInLocation("2006-01-02", last.In(loc).Format("2006-01-02"), loc)
		sinceDate, err2 := time.ParseInLocation("2006-01-02", off.sinceRaw, loc)
		if err1 != nil || err2 != nil || sinceDate.Before(lastResetDate) {
			return up, down
		}
	}
	return addUint64Saturating(up, off.up), addUint64Saturating(down, off.down)
}

func readTrafficOffset(path string) (trafficOffset, bool) {
	var off trafficOffset
	f, err := os.Open(path)
	if err != nil {
		return trafficOffset{}, false
	}
	defer f.Close()
	ok := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			return trafficOffset{}, false
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch k {
		case "up":
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return trafficOffset{}, false
			}
			off.up = n
			ok = true
		case "down":
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return trafficOffset{}, false
			}
			off.down = n
			ok = true
		case "reset_day":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 31 {
				return trafficOffset{}, false
			}
			off.resetDay = n
		case "since":
			t, err := time.Parse("2006-01-02", v)
			if err != nil || t.Format("2006-01-02") != v {
				return trafficOffset{}, false
			}
			off.since = t
			off.sinceRaw = v
		default:
			return trafficOffset{}, false
		}
	}
	if err := sc.Err(); err != nil || !ok {
		return trafficOffset{}, false
	}
	if off.resetDay > 0 && off.sinceRaw == "" {
		return trafficOffset{}, false
	}
	return off, true
}
