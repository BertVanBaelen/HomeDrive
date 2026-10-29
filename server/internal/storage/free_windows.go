//go:build windows

package storage

import "golang.org/x/sys/windows"

// DiskUsage is the size of the folder's disk and how many bytes this
// process can still write to it. Windows is only used for development.
func (s *Store) DiskUsage() (total, free uint64, err error) {
	p, err := windows.UTF16PtrFromString(s.path)
	if err != nil {
		return 0, 0, err
	}
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, nil); err != nil {
		return 0, 0, err
	}
	return total, free, nil
}
