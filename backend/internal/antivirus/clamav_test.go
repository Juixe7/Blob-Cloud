package antivirus

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestScanStreamFailsClosedWithoutAddress(t *testing.T) {
	clean, _, err := NewClamAVClient("").ScanStream(context.Background(), strings.NewReader("payload"))
	if err == nil {
		t.Fatal("expected missing scanner configuration to fail")
	}
	if clean {
		t.Fatal("unscanned payload was reported clean")
	}
}

func TestScanStreamClamAVIntegration(t *testing.T) {
	address := os.Getenv("CLAMAV_TEST_ADDRESS")
	if address == "" {
		t.Skip("set CLAMAV_TEST_ADDRESS to run against a live ClamAV service")
	}
	client := NewClamAVClient(address)
	clean, _, err := client.ScanStream(context.Background(), strings.NewReader("ordinary harmless text"))
	if err != nil || !clean {
		t.Fatalf("clean sample: clean=%v err=%v", clean, err)
	}
	const eicar = "X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*"
	clean, name, err := client.ScanStream(context.Background(), strings.NewReader(eicar))
	if err != nil {
		t.Fatalf("EICAR sample scan: %v", err)
	}
	if clean || name == "" || !strings.Contains(strings.ToLower(name), "eicar") {
		t.Fatalf("EICAR sample was not identified: clean=%v name=%q", clean, name)
	}
}
