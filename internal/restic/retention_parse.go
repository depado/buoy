package restic

import (
	"strconv"
	"strings"
)

func ParseRetentionPolicy(s string) RetentionPolicy {
	var rp RetentionPolicy
	if s == "" {
		return rp
	}
	for _, part := range SplitTrim(s) {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch key {
		case "keep-last":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepLast = n
			}
		case "keep-hourly":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepHourly = n
			}
		case "keep-daily":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepDaily = n
			}
		case "keep-weekly":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepWeekly = n
			}
		case "keep-monthly":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepMonthly = n
			}
		case "keep-yearly":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepYearly = n
			}
		case "keep-within":
			rp.KeepWithin = val
		}
	}
	return rp
}

func SplitTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
