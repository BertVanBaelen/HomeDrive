//go:build !windows

package storage

import (
	"errors"
	"math/bits"

	"golang.org/x/sys/unix"
)

var (
	errInvalidDiskStat  = errors.New("filesystem returned an invalid block count or size")
	errDiskSizeOverflow = errors.New("filesystem size overflows uint64")
)

type diskInt interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// diskBytes checks the signed and unsigned statfs fields before converting
// them, then checks the multiplication against uint64 overflow.
func diskBytes[B, S diskInt](blocks B, blockSize S) (uint64, error) {
	if blocks < 0 || blockSize <= 0 {
		return 0, errInvalidDiskStat
	}
	// #nosec G115 -- both signed values are non-negative, and the
	// multiplication below is checked separately.
	hi, bytes := bits.Mul64(uint64(blocks), uint64(blockSize))
	if hi != 0 {
		return 0, errDiskSizeOverflow
	}
	return bytes, nil
}

// DiskUsage is the size of the folder's disk and how many bytes this
// process can still write to it.
func (s *Store) DiskUsage() (total, free uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(s.path, &st); err != nil {
		return 0, 0, err
	}
	total, err = diskBytes(st.Blocks, st.Bsize)
	if err != nil {
		return 0, 0, err
	}
	free, err = diskBytes(st.Bavail, st.Bsize)
	if err != nil {
		return 0, 0, err
	}
	return total, free, nil
}
