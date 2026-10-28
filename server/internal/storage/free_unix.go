//go:build !windows

package storage

import "golang.org/x/sys/unix"

// FreeSpace is how many bytes can still be written to the folder's disk
// by this process.
func (s *Store) FreeSpace() (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(s.path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
