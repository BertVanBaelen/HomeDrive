//go:build !windows

package storage

import (
	"errors"
	"math"
	"testing"
)

func TestDiskBytes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		blocks    int64
		blockSize int64
		want      uint64
		wantErr   error
	}{
		{name: "bytes", blocks: 3, blockSize: 4096, want: 12_288},
		{name: "negative blocks", blocks: -1, blockSize: 4096, wantErr: errInvalidDiskStat},
		{name: "zero block size", blocks: 1, blockSize: 0, wantErr: errInvalidDiskStat},
		{name: "negative block size", blocks: 1, blockSize: -1, wantErr: errInvalidDiskStat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := diskBytes(tc.blocks, tc.blockSize)
			if !errors.Is(err, tc.wantErr) || got != tc.want {
				t.Fatalf("diskBytes(%d, %d) = %d, %v; want %d, %v", tc.blocks, tc.blockSize, got, err, tc.want, tc.wantErr)
			}
		})
	}

	if got, err := diskBytes(uint64(math.MaxUint64), uint64(2)); got != 0 || !errors.Is(err, errDiskSizeOverflow) {
		t.Fatalf("overflow = %d, %v; want 0, %v", got, err, errDiskSizeOverflow)
	}
}
