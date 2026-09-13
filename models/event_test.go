package models

import "testing"

// RFC3339 字符串只在偏移相同时字典序才等于时间序，比较必须先解析成时刻。
func TestCompareObservedAcrossOffsets(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  int
	}{
		{
			name:  "同一偏移按时间序",
			left:  "2026-09-14T02:00:00+08:00",
			right: "2026-09-14T01:00:00+08:00",
			want:  1,
		},
		{
			name:  "跨偏移按时刻：02:00Z 晚于 09:00+08:00",
			left:  "2026-09-14T02:00:00+00:00",
			right: "2026-09-14T09:00:00+08:00",
			want:  1,
		},
		{
			name:  "同一时刻不同偏移视为相等",
			left:  "2026-09-14T10:00:00+08:00",
			right: "2026-09-14T02:00:00+00:00",
			want:  0,
		},
		{
			name:  "负偏移",
			left:  "2026-09-14T01:00:00-05:00",
			right: "2026-09-14T02:00:00+00:00",
			want:  1,
		},
		{
			name:  "跨日界的偏移",
			left:  "2026-09-15T01:00:00+08:00",
			right: "2026-09-14T20:00:00+00:00",
			want:  -1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := Event{ObservedAt: test.left}
			right := Event{ObservedAt: test.right}

			if got := sign(CompareObserved(left, right)); got != test.want {
				t.Fatalf("CompareObserved(%s, %s) = %d，期望 %d", test.left, test.right, got, test.want)
			}

			// 反对称性：交换后符号应相反
			if got := sign(CompareObserved(right, left)); got != -test.want {
				t.Fatalf("交换后应为 %d，实际 %d", -test.want, got)
			}
		})
	}
}

// 畸形输入不应被静默判成同一时刻。
func TestCompareObservedToleratesMalformedStamps(t *testing.T) {
	valid := Event{ObservedAt: "2026-09-14T02:00:00+08:00"}
	malformed := Event{ObservedAt: "not-a-timestamp"}

	if got := CompareObserved(valid, malformed); got == 0 {
		t.Fatal("可解析与不可解析的时间不应被判为相等")
	}
}

func TestEffectiveObservedAtFallsBackToRecorded(t *testing.T) {
	tests := []struct {
		name string
		even Event
		want string
	}{
		{
			name: "显式给出时用它",
			even: Event{ObservedAt: "2026-09-14T02:00:00+08:00", RecordedAt: "2026-09-15T02:00:00+08:00"},
			want: "2026-09-14T02:00:00+08:00",
		},
		{
			name: "缺省时回退到记录时间",
			even: Event{RecordedAt: "2026-09-15T02:00:00+08:00"},
			want: "2026-09-15T02:00:00+08:00",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.even.EffectiveObservedAt(); got != test.want {
				t.Fatalf("EffectiveObservedAt() = %q，期望 %q", got, test.want)
			}
		})
	}
}

func sign(value int) int {
	switch {
	case value > 0:
		return 1
	case value < 0:
		return -1
	default:
		return 0
	}
}

// 读取端容忍畸形时间戳是刻意的，写入端则必须拒绝：
// 错误的时间戳会长期参与当前值判定，而调用方得不到任何提示。
func TestValidateObservedAt(t *testing.T) {
	tests := []struct {
		name    string
		stamp   string
		wantErr bool
	}{
		{"缺省允许，由写入路径补齐", "", false},
		{"带偏移", "2026-09-14T02:00:00+08:00", false},
		{"UTC", "2026-09-14T02:00:00Z", false},
		{"负偏移", "2026-09-14T02:00:00-05:00", false},
		{"月份越界", "2026-13-45T99:99:99+08:00", true},
		{"缺少时区", "2026-09-14T02:00:00", true},
		{"纯文本", "not-a-timestamp", true},
		{"仅日期", "2026-09-14", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Event{ObservedAt: test.stamp}.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate(%q) 返回 %v，期望出错=%v", test.stamp, err, test.wantErr)
			}
		})
	}
}
