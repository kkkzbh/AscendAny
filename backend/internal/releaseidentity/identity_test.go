package releaseidentity

import "testing"

func TestValidateReleaseIdentity(t *testing.T) {
	t.Parallel()
	for _, identity := range [][3]string{
		{"0.2.0", "0123456789abcdef0123456789abcdef01234567", "2026-07-13T04:00:00Z"},
		{"1.0.0-rc.1+km6", "abcdef0123456789abcdef0123456789abcdef01", "2026-07-13T04:00:00.123456789Z"},
	} {
		if err := Validate(identity[0], identity[1], identity[2]); err != nil {
			t.Fatalf("Validate(%q, %q, %q) error = %v", identity[0], identity[1], identity[2], err)
		}
	}
	for _, identity := range [][3]string{
		{"01.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-13T04:00:00Z"},
		{"0.2.0", "ABCDEF0123456789ABCDEF0123456789ABCDEF01", "2026-07-13T04:00:00Z"},
		{"0.2.0", "0123456789abcdef0123456789abcdef01234567", "2026-07-13T04:00:00+00:00"},
	} {
		if err := Validate(identity[0], identity[1], identity[2]); err == nil {
			t.Fatalf("Validate(%q, %q, %q) error = nil", identity[0], identity[1], identity[2])
		}
	}
}
