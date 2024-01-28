package restic

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/fs"
	"golang.org/x/sys/windows"
)

var (
	modAdvapi32     = syscall.NewLazyDLL("advapi32.dll")
	procEncryptFile = modAdvapi32.NewProc("EncryptFileW")
	procDecryptFile = modAdvapi32.NewProc("DecryptFileW")
)

// mknod is not supported on Windows.
func mknod(_ string, mode uint32, dev uint64) (err error) {
	return errors.New("device nodes cannot be created on windows")
}

// Windows doesn't need lchown
func lchown(_ string, uid int, gid int) (err error) {
	return nil
}

// restoreSymlinkTimestamps restores timestamps for symlinks
func (node Node) restoreSymlinkTimestamps(path string, utimes [2]syscall.Timespec) error {
	// tweaked version of UtimesNano from go/src/syscall/syscall_windows.go
	pathp, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	h, e := syscall.CreateFile(pathp,
		syscall.FILE_WRITE_ATTRIBUTES, syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return e
	}

	defer func() {
		err := syscall.Close(h)
		if err != nil {
			debug.Log("Error closing file handle for %s: %v\n", path, err)
		}
	}()

	a := syscall.NsecToFiletime(syscall.TimespecToNsec(utimes[0]))
	w := syscall.NsecToFiletime(syscall.TimespecToNsec(utimes[1]))
	return syscall.SetFileTime(h, nil, &a, &w)
}

// Getxattr retrieves extended attribute data associated with path.
func Getxattr(path, name string) ([]byte, error) {
	return nil, nil
}

// Listxattr retrieves a list of names of extended attributes associated with the
// given path in the file system.
func Listxattr(path string) ([]string, error) {
	return nil, nil
}

// Setxattr associates name and data together as an attribute of path.
func Setxattr(path, name string, data []byte) error {
	return nil
}

type statT syscall.Win32FileAttributeData

func toStatT(i interface{}) (*statT, bool) {
	s, ok := i.(*syscall.Win32FileAttributeData)
	if ok && s != nil {
		return (*statT)(s), true
	}
	return nil, false
}

func (s statT) dev() uint64   { return 0 }
func (s statT) ino() uint64   { return 0 }
func (s statT) nlink() uint64 { return 0 }
func (s statT) uid() uint32   { return 0 }
func (s statT) gid() uint32   { return 0 }
func (s statT) rdev() uint64  { return 0 }

func (s statT) size() int64 {
	return int64(s.FileSizeLow) | (int64(s.FileSizeHigh) << 32)
}

func (s statT) atim() syscall.Timespec {
	return syscall.NsecToTimespec(s.LastAccessTime.Nanoseconds())
}

func (s statT) mtim() syscall.Timespec {
	return syscall.NsecToTimespec(s.LastWriteTime.Nanoseconds())
}

func (s statT) ctim() syscall.Timespec {
	// Windows does not have the concept of a "change time" in the sense Unix uses it, so we're using the LastWriteTime here.
	return syscall.NsecToTimespec(s.LastWriteTime.Nanoseconds())
}

// restoreGenericAttributes restores generic attributes for Windows
func (node Node) restoreGenericAttributes(path string) (err error) {
	var errs []error
	for _, attr := range node.GenericAttributes {
		if errGen := restoreGenericAttribute(attr, path); errGen != nil {
			errs = append(errs, fmt.Errorf("error restoring generic attribute for: %s : %v", path, errGen))
		}
	}
	return errors.CombineErrors(errs...)
}

// fillGenericAttributes fills in the generic attributes for windows like File Attributes,
// Created time, Security Descriptor etc.
func (node *Node) fillGenericAttributes(path string, fi os.FileInfo, stat *statT) (allowExtended bool, err error) {
	if strings.Contains(filepath.Base(path), ":") {
		//Do not process for Alternate Data Streams in Windows
		// Also do not allow processing of extended attributes for ADS.
		return false, nil
	}
	if !strings.HasSuffix(filepath.Clean(path), `\`) {
		// Do not process file attributes and created time for windows directories like
		// C:, D:
		// Filepath.Clean(path) ends with '\' for Windows root drives only.

		// Add File Attributes
		node.appendGenericAttribute(getFileAttributes(stat.FileAttributes))

		//Add Creation Time
		node.appendGenericAttribute(GetCreationTime(fi, path))
	}

	if node.Type == "file" || node.Type == "dir" {
		sd, err := getSecurityDescriptor(path)
		if err == nil {
			//Add Security Descriptor
			node.appendGenericAttribute(sd)
		}
	}
	return true, err
}

// appendGenericAttribute appends a GenericAttribute to the node
func (node *Node) appendGenericAttribute(genericAttribute Attribute) {
	if genericAttribute.Name != "" {
		node.GenericAttributes = append(node.GenericAttributes, genericAttribute)
	}
}

// getFileAttributes gets the value for the GenericAttribute TypeFileAttribute
func getFileAttributes(fileattr uint32) (fileAttribute Attribute) {
	fileAttrData := UInt32ToBytes(fileattr)
	return NewGenericAttribute(TypeFileAttribute, fileAttrData)
}

// UInt32ToBytes converts a uint32 value to a byte array
func UInt32ToBytes(value uint32) (bytes []byte) {
	bytes = make([]byte, 4)
	binary.LittleEndian.PutUint32(bytes, value)
	return bytes
}

// GetCreationTime gets the value for the GenericAttribute TypeCreationTime in a windows specific time format.
// The value is a 64-bit value representing the number of 100-nanosecond intervals since January 1, 1601 (UTC)
// split into two 32-bit parts: the low-order DWORD and the high-order DWORD for efficiency and interoperability.
// The low-order DWORD represents the number of 100-nanosecond intervals elapsed since January 1, 1601, modulo
// 2^32. The high-order DWORD represents the number of times the low-order DWORD has overflowed.
func GetCreationTime(fi os.FileInfo, path string) (creationTimeAttribute Attribute) {
	attrib, success := fi.Sys().(*syscall.Win32FileAttributeData)
	if success && attrib != nil {
		var creationTime [8]byte
		binary.LittleEndian.PutUint32(creationTime[0:4], attrib.CreationTime.LowDateTime)
		binary.LittleEndian.PutUint32(creationTime[4:8], attrib.CreationTime.HighDateTime)
		creationTimeAttribute = NewGenericAttribute(TypeCreationTime, creationTime[:])
	} else {
		debug.Log("Could not get create time for path: %s", path)
	}
	return creationTimeAttribute
}

// getSecurityDescriptor function retrieves the GenericAttribute containing the byte representation
// of the Security Descriptor. This byte representation is obtained from the encoded string form of
// the raw binary Security Descriptor associated with the Windows file or folder.
func getSecurityDescriptor(path string) (sdAttribute Attribute, err error) {
	sd, err := fs.GetFileSecurityDescriptor(path)
	if err != nil {
		//If backup privilege was already enabled, then this is not an initialization issue as admin permission would be needed for this step.
		//This is a specific error, logging it in debug for now.
		err = fmt.Errorf("Error getting file SecurityDescriptor for: %s : %v", path, err)
		debug.Log("%v", err)
		return sdAttribute, err
	} else if sd != "" {
		sdAttribute = NewGenericAttribute(TypeSecurityDescriptor, []byte(sd))
	}
	return sdAttribute, nil
}

// restoreGenericAttribute restores the generic attributes for Windows like File Attributes,
// Created time, Security Descriptor etc.
func restoreGenericAttribute(attr Attribute, path string) error {
	switch attr.Name {
	case string(TypeFileAttribute):
		return handleFileAttributes(path, attr.Value)
	case string(TypeCreationTime):
		return handleCreationTime(path, attr.Value)
	case string(TypeSecurityDescriptor):
		return handleSecurityDescriptor(path, attr.Value)
	}
	handleUnknownGenericAttributeFound(attr.Name)
	return nil
}

// handleFileAttributes gets the File Attributes from the data and sets them to the file/folder
// at the specified path.
func handleFileAttributes(path string, data []byte) (err error) {
	attrs := binary.LittleEndian.Uint32(data)
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	err = fixEncryptionAttribute(path, attrs, pathPointer)
	if err != nil {
		debug.Log("Could not change encryption attribute for path: %s: %v", path, err)
	}
	return syscall.SetFileAttributes(pathPointer, attrs)
}

// fixEncryptionAttribute checks if a file needs to be marked encrypted and is not already encrypted, it sets
// the FILE_ATTRIBUTE_ENCRYPTED. Conversely, if the file needs to be marked unencrypted and it is already
// marked encrypted, it removes the FILE_ATTRIBUTE_ENCRYPTED.
func fixEncryptionAttribute(path string, attrs uint32, pathPointer *uint16) (err error) {
	if attrs&windows.FILE_ATTRIBUTE_ENCRYPTED != 0 {
		// File should be encrypted.
		err = encryptFile(pathPointer)
		if err != nil {
			if fs.IsAccessDenied(err) {
				// If existing file already has readonly or system flag, encrypt file call fails.
				// We have already cleared readonly flag, clearing system flag if needed.
				// The readonly and system flags will be set again at the end of this func if they are needed.
				err = fs.ClearSystem(path)
				if err != nil {
					return fmt.Errorf("failed to encrypt file: failed to clear system flag: %s : %v", path, err)
				}
				err = encryptFile(pathPointer)
				if err != nil {
					return fmt.Errorf("failed to encrypt file: %s : %v", path, err)
				}
			} else {
				return fmt.Errorf("failed to encrypt file: %s : %v", path, err)
			}
		}
	} else {
		existingAttrs, err := windows.GetFileAttributes(pathPointer)
		if err != nil {
			return fmt.Errorf("failed to get file attributes for existing file: %s : %v", path, err)
		}
		if existingAttrs&windows.FILE_ATTRIBUTE_ENCRYPTED != 0 {
			// File should not be encrypted, but its already encrypted. Decrypt it.
			err = decryptFile(pathPointer)
			if err != nil {
				if fs.IsAccessDenied(err) {
					// If existing file already has readonly or system flag, decrypt file call fails.
					// We have already cleared readonly flag, clearing system flag if needed.
					// The readonly and system flags will be set again after this func if they are needed.
					err = fs.ClearSystem(path)
					if err != nil {
						return fmt.Errorf("failed to decrypt file: failed to clear system flag: %s : %v", path, err)
					}
					err = decryptFile(pathPointer)
					if err != nil {
						return fmt.Errorf("failed to decrypt file: %s : %v", path, err)
					}
				} else {
					return fmt.Errorf("failed to decrypt file: %s : %v", path, err)
				}
			}
		}
	}
	return err
}

// encryptFile set the encrypted flag on the file.
func encryptFile(pathPointer *uint16) error {
	// Call EncryptFile function
	ret, _, err := procEncryptFile.Call(uintptr(unsafe.Pointer(pathPointer)))
	if ret == 0 {
		return err
	}
	return nil
}

// decryptFile removes the encrypted flag from the file.
func decryptFile(pathPointer *uint16) error {
	// Call DecryptFile function
	ret, _, err := procDecryptFile.Call(uintptr(unsafe.Pointer(pathPointer)))
	if ret == 0 {
		return err
	}
	return nil
}

// handleCreationTime gets the creation time from the data and sets it to the file/folder at
// the specified path.
func handleCreationTime(path string, data []byte) (err error) {
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(pathPointer,
		syscall.FILE_WRITE_ATTRIBUTES, syscall.FILE_SHARE_WRITE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer func() {
		err := syscall.Close(handle)
		if err != nil {
			debug.Log("Error closing file handle for %s: %v\n", path, err)
		}
	}()

	var creationTime syscall.Filetime
	creationTime.LowDateTime = binary.LittleEndian.Uint32(data[0:4])
	creationTime.HighDateTime = binary.LittleEndian.Uint32(data[4:8])
	return syscall.SetFileTime(handle, &creationTime, nil, nil)
}

// handleSecurityDescriptor gets the Security Descriptor from the data and sets it to the file/folder at
// the specified path.
func handleSecurityDescriptor(path string, data []byte) error {
	return fs.SetFileSecurityDescriptor(path, string(data))
}
