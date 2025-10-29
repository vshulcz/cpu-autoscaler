package metrics

import (
	"strings"
	"unicode"
)

func AggregateCPUPercent(gauges map[string]float64, cpuKeyPrefix string) (float64, error) {
	if len(gauges) == 0 {
		return 0, ErrNoCPUKeys
	}
	var sum float64
	var n int
	for k, v := range gauges {
		if !strings.HasPrefix(k, cpuKeyPrefix) {
			continue
		}
		suffix := k[len(cpuKeyPrefix):]
		if len(suffix) == 0 || !allDigits(suffix) {
			continue
		}
		if v < 0 || v > 100 {
			continue
		}
		sum += v
		n++
	}
	if n == 0 {
		if hasPrefix(gauges, cpuKeyPrefix) {
			return 0, ErrNoValidCPU
		}
		return 0, ErrNoCPUKeys
	}
	return sum / float64(n), nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func hasPrefix(m map[string]float64, p string) bool {
	for k := range m {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}
