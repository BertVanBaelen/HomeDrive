//go:build !windows

package storage

import "golang.org/x/sys/unix"

// DiskUsage is the size of the folder's disk and how many bytes this
// process can still write to it.
func (s *Store) DiskUsage() (total, free uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(s.path, &st); err != nil {
		return 0, 0, err
	}
	return st.Blocks * uint64(st.Bsize), st.Bavail * uint64(st.Bsize), nil
}
