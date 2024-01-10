package restic

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
)

// mknod is not supported on Windows.
func mknod(_ string, mode uint32, dev uint64) (err error) {
	return errors.New("device nodes cannot be created on windows")
}

// Windows doesn't need lchown
func lchown(_ string, uid int, gid int) (err error) {
	return nil
}

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

// restoreGenericAttributes restores generic attributes for windows
func (node Node) restoreGenericAttributes(path string) (err error) {
	for _, attr := range node.GenericAttributes {
		if errGen := attr.restoreGenericAttribute(path); errGen != nil {
			err = fmt.Errorf("Error restoring generic attribute for: %s : %v", path, errGen)
			debug.Log("%v", err)
		}
	}
	return err
}

// fillGenericAttributes fills in the generic attributes for windows like FileAttributes,
// Created time and SecurityDescriptor.
func (node *Node) fillGenericAttributes(path string, fi os.FileInfo, stat *statT) (allowExtended bool, err error) {
	if strings.Contains(filepath.Base(path), ":") || strings.HasSuffix(filepath.Clean(path), `\`) {
		//Do not process for windows directories like C:, D: and for Alternate Data Streams in Windows
		//Filepath.Clean(path) ends with '\' for Windows root drives only.
		// Also do not allow to process extended attributes.
		return false, nil
	}
	// Add File Attributes
	node.appendGenericAttribute(getFileAttributes(stat.FileAttributes))

	//Add Creation Time
	node.appendGenericAttribute(getCreationTime(fi, path))
	return true, err
}

func (node *Node) appendGenericAttribute(genericAttribute GenericAttribute) {
	if genericAttribute.Name != "" {
		node.GenericAttributes = append(node.GenericAttributes, genericAttribute)
	}
}

func getFileAttributes(fileattr uint32) (fileAttribute GenericAttribute) {
	fileAttrData := make([]byte, 4)
	binary.LittleEndian.PutUint32(fileAttrData, fileattr)
	fileAttribute = NewGenericAttribute(TypeFileAttribute, fileAttrData)
	return fileAttribute
}

func getCreationTime(fi os.FileInfo, path string) (creationTimeAttribute GenericAttribute) {
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

// restoreGenericAttribute restores the generic attributes for windows like File Attributes,
// Created time and Security Descriptors.
func (attr GenericAttribute) restoreGenericAttribute(path string) error {
	switch attr.Name {
	case string(TypeFileAttribute):
		return handleFileAttributes(path, attr.Value)
	case string(TypeCreationTime):
		return handleCreationTime(path, attr.Value)
	}
	handleUnknownGenericAttributeFound(attr.Name)
	return nil
}

func handleFileAttributes(path string, data []byte) (err error) {
	attrs := binary.LittleEndian.Uint32(data)
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return syscall.SetFileAttributes(pathPointer, attrs)
}

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

	var inputData bytes.Buffer
	inputData.Write(data)

	var creationTime syscall.Filetime
	creationTime.LowDateTime = binary.LittleEndian.Uint32(data[0:4])
	creationTime.HighDateTime = binary.LittleEndian.Uint32(data[4:8])
	if err := syscall.SetFileTime(handle, &creationTime, nil, nil); err != nil {
		return err
	}
	return nil
}
