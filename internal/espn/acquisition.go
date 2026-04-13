package espn

import "strings"

const (
	AcquisitionStatusFreeAgent = "FREEAGENT"
	AcquisitionStatusWaivers   = "WAIVERS"
)

func NormalizeAcquisitionStatus(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case AcquisitionStatusFreeAgent:
		return AcquisitionStatusFreeAgent
	case AcquisitionStatusWaivers:
		return AcquisitionStatusWaivers
	default:
		return ""
	}
}

func IsImmediateFreeAgent(status string) bool {
	normalized := NormalizeAcquisitionStatus(status)
	// Backward compatibility for older rows that predate acquisition_status.
	return normalized == "" || normalized == AcquisitionStatusFreeAgent
}

func IsWaiver(status string) bool {
	return NormalizeAcquisitionStatus(status) == AcquisitionStatusWaivers
}
