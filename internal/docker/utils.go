package docker

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/depado/buoy/internal/types"
)

func getString(labels map[string]string, key, fallback string) string {
	if v, ok := labels[key]; ok {
		return v
	}
	return fallback
}

func getSlice(labels map[string]string, key string) []string {
	if v, ok := labels[key]; ok {
		return types.SplitTrim(v)
	}
	return nil
}

func getBool(labels map[string]string, key string, fallback bool) bool {
	v, ok := labels[key]
	if !ok {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		slog.Warn("invalid "+key+", using default", "value", v)
		return fallback
	}
	return b
}

func getDuration(labels map[string]string, key string, fallback time.Duration) time.Duration {
	v, ok := labels[key]
	if !ok {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("invalid "+key+", using default", "value", v)
		return fallback
	}
	return d
}

func setNonZero[T comparable](dst *T, src T) {
	var zero T
	if src != zero {
		*dst = src
	}
}
