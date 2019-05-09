package multibuf

import (
	"reflect"
	"testing"
)

func Test_bufsIndex(t *testing.T) {
	type args struct {
		index int
		buf   []byte
		bufs  [][]byte
	}
	tests := []struct {
		name  string
		args  args
		want  int
		want1 int
	}{
		{
			name: "Test first success",
			args: args{
				index: 0,
				buf:   []byte{0, 1, 2},
				bufs: [][]byte{
					[]byte{3, 4},
				},
			},
			want:  0,
			want1: 0,
		},
		{
			name: "Test first with empty buffers",
			args: args{
				index: 0,
			},
			want:  -1,
			want1: -1,
		},
		{
			name: "Test negative index",
			args: args{
				index: -1,
				buf:   []byte{0, 1, 2},
				bufs: [][]byte{
					[]byte{3, 4},
				},
			},
			want:  -1,
			want1: -1,
		},
		{
			name: "Test out of bounds index",
			args: args{
				index: 256,
				buf:   []byte{0, 1, 2},
				bufs: [][]byte{
					[]byte{3, 4},
				},
			},
			want:  -1,
			want1: -1,
		},
		{
			name: "Test index 0 of buf1",
			args: args{
				index: 3,
				buf:   []byte{0, 1, 2},
				bufs: [][]byte{
					[]byte{3, 4},
				},
			},
			want:  1,
			want1: 0,
		},
		{
			name: "Test index len(buf0)-1 of buf0",
			args: args{
				index: 2,
				buf:   []byte{0, 1, 2},
				bufs: [][]byte{
					[]byte{3, 4},
				},
			},
			want:  0,
			want1: 2,
		},
		{
			name: "Test index len(buf1)-1 of buf1",
			args: args{
				index: 4,
				buf:   []byte{0, 1, 2},
				bufs: [][]byte{
					[]byte{3, 4},
				},
			},
			want:  1,
			want1: 1,
		},
		{
			name: "Test out of bounds by 1",
			args: args{
				index: 5,
				buf:   []byte{0, 1, 2},
				bufs: [][]byte{
					[]byte{3, 4},
				},
			},
			want:  -1,
			want1: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := Index(tt.args.index, tt.args.buf, tt.args.bufs...)
			if got != tt.want {
				t.Errorf("bufsIndex() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("bufsIndex() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func Test_bufsPatternIndex(t *testing.T) {
	type args struct {
		pattern []byte
		buf     []byte
		bufs    [][]byte
	}
	tests := []struct {
		name string
		args args
		want int
	}{
		{
			name: "empty pattern matches nothing",
			args: args{
				pattern: []byte(""),
				buf:     []byte("haystack"),
				bufs: [][]byte{
					[]byte("needleneedle"),
				},
			},
			want: -1,
		},
		{
			name: "perfect match at index 0",
			args: args{
				pattern: []byte("needle"),
				buf:     []byte("needle"),
				bufs: [][]byte{
					[]byte("needleneedle"),
				},
			},
			want: 0,
		},
		{
			name: "match at index 0",
			args: args{
				pattern: []byte("needle"),
				buf:     []byte("needlehaystack"),
				bufs: [][]byte{
					[]byte("needleneedle"),
				},
			},
			want: 0,
		},
		{
			name: "match at end of first buffer",
			args: args{
				pattern: []byte("needle"),
				buf:     []byte("haystackneedle"),
				bufs: [][]byte{
					[]byte("needleneedle"),
				},
			},
			want: 8,
		},
		{
			name: "match buf1 at index 0",
			args: args{
				pattern: []byte("needle"),
				buf:     []byte("haystack"),
				bufs: [][]byte{
					[]byte("needleneedle"),
				},
			},
			want: 8,
		},
		{
			name: "match buf1 at end",
			args: args{
				pattern: []byte("needle"),
				buf:     []byte("haystack"),
				bufs: [][]byte{
					[]byte("needlrneedle"),
				},
			},
			want: 14,
		},
		{
			name: "match buf2 middle",
			args: args{
				pattern: []byte("deedle"),
				buf:     []byte("haystack"),
				bufs: [][]byte{
					[]byte("needlrneedle"),
					[]byte("sweedledeedlebeatle"),
				},
			},
			want: 27,
		},
		{
			name: "match acros buffer boundary",
			args: args{
				pattern: []byte("dleswe"),
				buf:     []byte("haystack"),
				bufs: [][]byte{
					[]byte("needlrneedle"),
					[]byte("sweedledeedlebeatle"),
				},
			},
			want: 17,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if "match buf2 middle" == tt.name {
				println("breakpoint")
			}
			if got := PatternIndex(tt.args.pattern, tt.args.buf, tt.args.bufs...); got != tt.want {
				t.Errorf("bufsPatternIndex() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_bufsSlice(t *testing.T) {
	type args struct {
		index     int
		uptoIndex int
		buf       []byte
		bufs      [][]byte
	}
	tests := []struct {
		name string
		args args
		want []byte
	}{
		{
			name: "slice first byte",
			args: args{
				buf: []byte("abbbb"),
				bufs: [][]byte{
					[]byte("cccc"),
				},
				index:     0,
				uptoIndex: 1,
			},
			want: []byte("a"),
		},
		{
			name: "slice first buffer whole",
			args: args{
				buf: []byte("abbbb"),
				bufs: [][]byte{
					[]byte("cccc"),
				},
				index:     0,
				uptoIndex: 5,
			},
			want: []byte("abbbb"),
		},
		{
			name: "negative length goes to the end",
			args: args{
				buf: []byte("abbbb"),
				bufs: [][]byte{
					[]byte("cccc"),
				},
				index:     0,
				uptoIndex: -1,
			},
			want: []byte("abbbbcccc"),
		},
		{
			name: "first byte of second buffer",
			args: args{
				buf: []byte("abbbb"),
				bufs: [][]byte{
					[]byte("cccc"),
				},
				index:     5,
				uptoIndex: 6,
			},
			want: []byte("c"),
		},
		{
			name: "slice across buf boundary",
			args: args{
				buf: []byte("000b"),
				bufs: [][]byte{
					[]byte("c1111"),
				},
				index:     3,
				uptoIndex: 5,
			},
			want: []byte("bc"),
		},
		{
			name: "slice of second buffer",
			args: args{
				buf: []byte("000b"),
				bufs: [][]byte{
					[]byte("c1111"),
				},
				index:     5,
				uptoIndex: 9,
			},
			want: []byte("1111"),
		},
		{
			name: "slice of second buffer 2",
			args: args{
				buf: []byte("000b"),
				bufs: [][]byte{
					[]byte("c1111"),
				},
				index:     6,
				uptoIndex: 9,
			},
			want: []byte("111"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Slice(tt.args.index, tt.args.uptoIndex, tt.args.buf, tt.args.bufs...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("bufsSlice() = %v, want %v", got, tt.want)
			}
		})
	}
}
