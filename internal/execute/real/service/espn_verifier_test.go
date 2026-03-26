package service

import "testing"

func TestInferExecutionOutcomeAddOnlyNextDayPending(t *testing.T) {
	inference, _ := InferExecutionOutcome(false, false, true, true)
	if inference != "inconclusive" {
		t.Fatalf("expected inconclusive for add-only next-day when not yet visible, got %s", inference)
	}
}
