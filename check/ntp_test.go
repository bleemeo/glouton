package check

import "testing"

func TestLeapVersionMode(t *testing.T) {
	v := encodeLeapVersionMode(0, 3, 3)
	if v != 0x1b {
		t.Errorf("encodeLeapVersionMode() == %v, want %v", v, 0x1b)
	}
	li, vn, mode := decodeLeapVersionMode(0x1b)
	if li != 0 {
		t.Errorf("leap indicator == %v, want 0", li)
	}
	if vn != 3 {
		t.Errorf("version number == %v, want 0", vn)
	}
	if mode != 3 {
		t.Errorf("mode == %v, want 0", mode)
	}
}
