package main

import (
	"fmt"
	"testing"
)

func TestLowestCost(t *testing.T) {
	bpdu1 := BPDU{
		RootID:   "0129",
		Cost:     1,
		BridgeID: "9e4a"}

	bpdu2 := BPDU{
		RootID:   "0129",
		Cost:     2,
		BridgeID: "8824"}
	bpdus := []BPDU{bpdu2, bpdu1}
	val := lowestCost(bpdus, "0129")
	fmt.Println(val)
	if val != bpdu1 {
		t.FailNow()
	}
}
