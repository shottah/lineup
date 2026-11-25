package docstore

import "testing"

func TestNowIsMicrosecondTruncated(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if n := now(); n.Nanosecond()%1000 != 0 {
			t.Fatalf("now() has sub-microsecond precision: %d ns", n.Nanosecond())
		}
	}
}
