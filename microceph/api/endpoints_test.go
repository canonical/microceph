package api

import "testing"

func TestEndpointsHaveUniquePaths(t *testing.T) {
	seen := make(map[string]bool)
	for _, endpoint := range Endpoints {
		if seen[endpoint.Path] {
			t.Fatalf("application endpoint %q is registered more than once", endpoint.Path)
		}
		seen[endpoint.Path] = true
	}
	if !seen[rgwServiceCmd.Path] {
		t.Fatal("RGW service endpoint is missing")
	}
}
