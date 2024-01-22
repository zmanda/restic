package fs

import (
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// fixpath returns an absolute path on windows, so restic can open long file
// names.
func fixpath(name string) string {
	abspath, err := filepath.Abs(name)
	if err == nil {
		// Check if \\?\UNC\ already exist
		if strings.HasPrefix(abspath, `\\?\UNC\`) {
			return abspath
		}
		// Check if \\?\ already exist
		if strings.HasPrefix(abspath, `\\?\`) {
			return abspath
		}
		// Check if path starts with \\
		if strings.HasPrefix(abspath, `\\`) {
			return strings.Replace(abspath, `\\`, `\\?\UNC\`, 1)
		}
		// Normal path
		return `\\?\` + abspath
	}
	return name
}

// TempFile creates a temporary file which is marked as delete-on-close
func TempFile(dir, prefix string) (f *os.File, err error) {
	// slightly modified implementation of os.CreateTemp(dir, prefix) to allow us to add
	// the FILE_ATTRIBUTE_TEMPORARY | FILE_FLAG_DELETE_ON_CLOSE flags.
	// These provide two large benefits:
	// FILE_ATTRIBUTE_TEMPORARY tells Windows to keep the file in memory only if possible
	// which reduces the amount of unnecessary disk writes.
	// FILE_FLAG_DELETE_ON_CLOSE instructs Windows to automatically delete the file once
	// all file descriptors are closed.

	if dir == "" {
		dir = os.TempDir()
	}

	access := uint32(windows.GENERIC_READ | windows.GENERIC_WRITE)
	creation := uint32(windows.CREATE_NEW)
	share := uint32(0) // prevent other processes from accessing the file
	flags := uint32(windows.FILE_ATTRIBUTE_TEMPORARY | windows.FILE_FLAG_DELETE_ON_CLOSE)

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < 10000; i++ {
		randSuffix := strconv.Itoa(int(1e9 + rnd.Intn(1e9)%1e9))[1:]
		path := filepath.Join(dir, prefix+randSuffix)

		ptr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(ptr, access, share, nil, creation, flags, 0)
		if os.IsExist(err) {
			continue
		}
		return os.NewFile(uintptr(h), path), err
	}

	// Proper error handling is still to do
	return nil, os.ErrExist
}

// Chmod changes the mode of the named file to mode.
func Chmod(name string, mode os.FileMode) error {
	return os.Chmod(fixpath(name), mode)
}

// SanitizeMainFileName will only keep the main file and remove the secondary file like ADS from the name.
func SanitizeMainFileName(str string) string {
	// The ADS is essentially a part of the main file. So for any functionality that
	// needs to consider the main file, like filtering, we need to derive the main file name
	// from the ADS name.
	return TrimAds(str)
}

// IsAccessDenied checks if the error is ERROR_ACCESS_DENIED or a Path error due to windows.ERROR_ACCESS_DENIED.
func IsAccessDenied(err error) bool {
	isAccessDenied := IsAccessDeniedError(err)
	if !isAccessDenied {
		if e, ok := err.(*os.PathError); ok {
			isAccessDenied = IsAccessDeniedError(e.Err)
		}
	}
	return isAccessDenied
}

// IsAccessDeniedError checks if the error is ERROR_ACCESS_DENIED.
func IsAccessDeniedError(err error) bool {
	return IsErrorOfType(err, windows.ERROR_ACCESS_DENIED)
}

// IsReadonly checks if the fileAtributes have readonly bit.
func IsReadonly(fileAttributes uint32) bool {
	return fileAttributes&windows.FILE_ATTRIBUTE_READONLY != 0
}

// ClearReadonly removes the readonly flag from the main file.
func ClearReadonly(isAds bool, path string) error {
	if isAds {
		// If this is an ads stream we need to get the main file for setting attributes.
		path = TrimAds(path)
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	fileAttributes, err := windows.GetFileAttributes(ptr)
	if err != nil {
		return err
	}
	if IsReadonly(fileAttributes) {
		// Clear FILE_ATTRIBUTE_READONLY flag
		fileAttributes &= ^uint32(windows.FILE_ATTRIBUTE_READONLY)
		err = windows.SetFileAttributes(ptr, fileAttributes)
		if err != nil {
			return err
		}
	}
	return nil
}
