package main

import "testing"

// 阈值与 100 题整卷标定对应：95 档 ≥80 题/95%、80 档 ≥50 题/80%、60 档 ≥30 题/60%
func TestAutoTargetMastery(t *testing.T) {
	cases := []struct {
		name string
		s    nodeStats
		want float64
	}{
		{"无作答", nodeStats{0, 0}, 0},
		{"题量不足", nodeStats{29, 28}, 0},
		{"达题量但正确率不足", nodeStats{40, 23}, 0},
		{"60档", nodeStats{30, 18}, 60},
		{"80档", nodeStats{50, 40}, 80},
		{"题量够但只到60线", nodeStats{50, 35}, 60},
		{"95档", nodeStats{80, 76}, 95},
		{"题量够但率只到80线", nodeStats{90, 85}, 80},
		{"整卷满分", nodeStats{100, 100}, 95},
	}
	for _, c := range cases {
		if got := autoTargetMastery(c.s); got != c.want {
			t.Errorf("%s: autoTargetMastery(%+v) = %v, want %v", c.name, c.s, got, c.want)
		}
	}
}
