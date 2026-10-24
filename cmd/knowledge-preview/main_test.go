package main

import "testing"

func TestPreviewRejectsImplicitOrRelativeStorage(t *testing.T) {
	for _, path := range []string{"", "relative"} {
		if err := run(path, "127.0.0.1:11437"); err == nil {
			t.Fatalf("accepted storage %q", path)
		}
	}
}

func TestPreviewRejectsNetworkExposure(t *testing.T) {
	for _, address := range []string{"0.0.0.0:11437", ":11437", "192.168.1.5:11437", "localhost:11437", "bad"} {
		if err := run(t.TempDir(), address); err == nil {
			t.Fatalf("accepted listen %q", address)
		}
	}
}
