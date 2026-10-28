//go:build windows

package storage

import "golang.org/x/sys/windows"

// FreeSpace is how many bytes can still be written to the folder's disk
// by this process. Windows is only used for development.
func (s *Store) FreeSpace() (uint64, error) {
	p, err := windows.UTF16PtrFromString(s.path)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}
