package main

import "testing"

func TestMountsContain(t *testing.T) {
	const sample = `proc /proc proc rw,relatime 0 0
/dev/mmcblk0p2 / ext4 ro,relatime 0 0
/dev/mmcblk0p4 /data ext4 rw,relatime 0 0
tmpfs /run tmpfs rw,nosuid 0 0
`
	tests := []struct {
		name   string
		mounts string
		target string
		want   bool
	}{
		{"data mounted", sample, "/data", true},
		{"root mounted", sample, "/", true},
		{"not mounted", sample, "/boot", false},
		{"empty mounts", "", "/data", false},
		{"prefix is not a match", "/dev/x /database ext4 rw 0 0\n", "/data", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mountsContain(tc.mounts, tc.target); got != tc.want {
				t.Errorf("mountsContain(%q, %q) = %v, want %v", tc.mounts, tc.target, got, tc.want)
			}
		})
	}
}
