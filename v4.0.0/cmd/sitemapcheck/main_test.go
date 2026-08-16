package main

import "testing"

func TestAcceptableBalancedDriftIsLocalBoundedAndSymmetric(t *testing.T) {
	if !acceptableBalancedDrift(2012, 2012, 2, 2, 0, 2) {
		t.Fatal("bounded balanced drift should be explainable locally")
	}
	for _, testCase := range []struct{ oldTotal, newTotal, missing, extra, metadata, allowance int }{
		{2012, 2012, 2, 2, 0, 0},
		{2012, 2011, 2, 2, 0, 2},
		{2012, 2012, 3, 3, 0, 2},
		{2012, 2012, 2, 1, 0, 2},
		{2012, 2012, 2, 2, 1, 2},
	} {
		if acceptableBalancedDrift(testCase.oldTotal, testCase.newTotal, testCase.missing, testCase.extra, testCase.metadata, testCase.allowance) {
			t.Fatalf("unsafe drift accepted: %+v", testCase)
		}
	}
}
