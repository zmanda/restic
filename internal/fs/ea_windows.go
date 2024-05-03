//go:build windows
// +build windows

package fs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GetFileEA retrieves the extended attributes for the file represented by `handle`. The
// `handle` must have been opened with file access flag FILE_READ_EA (0x8).
// The extended file attribute names in windows are case-insensitive and when fetching
// the attributes the names are generally returned in UPPER case.
func GetFileEA(handle windows.Handle) ([]ExtendedAttribute, error) {
	// default buffer size to start with
	bufLen := 1024
	buf := make([]byte, bufLen)
	var iosb ioStatusBlock
	// keep increasing the buffer size until it is large enough
	for {
		status := getFileEA(handle, &iosb, &buf[0], uint32(bufLen), false, 0, 0, nil, true)

		if status == noExtendedAttribsStatus {
			//If status is -1073741742, no extended attributes were found
			return nil, nil
		}
		err := status.Err()
		if err != nil {
			// convert ntstatus code to windows error
			if err == windows.ERROR_INSUFFICIENT_BUFFER || err == windows.ERROR_MORE_DATA {
				bufLen *= 2
				buf = make([]byte, bufLen)
				continue
			}
			return nil, fmt.Errorf("get file EA failed with: %w", err)
		}
		break
	}
	return DecodeExtendedAttributes(buf)
}

// SetFileEA sets the extended attributes for the file represented by `handle`.  The
// handle must have been opened with the file access flag FILE_WRITE_EA(0x10).
func SetFileEA(handle windows.Handle, attrs []ExtendedAttribute) error {
	encodedEA, err := EncodeExtendedAttributes(attrs)
	if err != nil {
		return fmt.Errorf("failed to encoded extended attributes: %w", err)
	}

	var iosb ioStatusBlock

	return setFileEA(handle, &iosb, &encodedEA[0], uint32(len(encodedEA))).Err()
}

func getFileEA(handle windows.Handle, iosb *ioStatusBlock, buf *uint8, bufLen uint32, returnSingleEntry bool, eaList uintptr, eaListLen uint32, eaIndex *uint32, restartScan bool) (status ntStatus) {
	var _p0 uint32
	if returnSingleEntry {
		_p0 = 1
	}
	var _p1 uint32
	if restartScan {
		_p1 = 1
	}
	r0, _, _ := syscall.SyscallN(procNtQueryEaFile.Addr(), uintptr(handle), uintptr(unsafe.Pointer(iosb)), uintptr(unsafe.Pointer(buf)), uintptr(bufLen), uintptr(_p0), uintptr(eaList), uintptr(eaListLen), uintptr(unsafe.Pointer(eaIndex)), uintptr(_p1))
	status = ntStatus(r0)
	return
}

func setFileEA(handle windows.Handle, iosb *ioStatusBlock, buf *uint8, bufLen uint32) (status ntStatus) {
	r0, _, _ := syscall.SyscallN(procNtSetEaFile.Addr(), uintptr(handle), uintptr(unsafe.Pointer(iosb)), uintptr(unsafe.Pointer(buf)), uintptr(bufLen), 0, 0)
	status = ntStatus(r0)
	return
}

// The code below was adapted from github.com/Microsoft/go-winio under MIT license.

// The MIT License (MIT)

// Copyright (c) 2015 Microsoft

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

//sys getFileEA(handle windows.Handle, iosb *ioStatusBlock, buf *uint8, bufLen uint32, returnSingleEntry bool, eaList uintptr, eaListLen uint32, eaIndex *uint32, restartScan bool) (status ntstatus) = ntdll.NtQueryEaFile
//sys setFileEA(handle windows.Handle, iosb *ioStatusBlock, buf *uint8, bufLen uint32) (status ntstatus) = ntdll.NtSetEaFile

var (
	fileFullEaInformationSize = binary.Size(&fileFullEaInformation{})

	errInvalidEaBuffer = errors.New("invalid extended attribute buffer")
	errEaNameTooLarge  = errors.New("extended attribute name too large")
	errEaValueTooLarge = errors.New("extended attribute value too large")

	modntdll                       = windows.NewLazySystemDLL("ntdll.dll")
	procNtQueryEaFile              = modntdll.NewProc("NtQueryEaFile")
	procNtSetEaFile                = modntdll.NewProc("NtSetEaFile")
	procRtlNtStatusToDosErrorNoTeb = modntdll.NewProc("RtlNtStatusToDosErrorNoTeb")
)

const (
	// noExtendedAttribsStatus is a constant value which indicates no extended attributes were found
	noExtendedAttribsStatus = -1073741742
)

type ntStatus int32

// ExtendedAttribute represents a single Windows EA.
type ExtendedAttribute struct {
	Name  string
	Value []byte
	Flags uint8
}

type fileFullEaInformation struct {
	NextEntryOffset uint32
	Flags           uint8
	NameLength      uint8
	ValueLength     uint16
}

// ioStatusBlock represents the IO_STATUS_BLOCK struct defined here: https://docs.microsoft.com/en-us/windows-hardware/drivers/ddi/wdm/ns-wdm-_io_status_block
type ioStatusBlock struct {
	Status, Information uintptr
}

func parseEa(b []byte) (ea ExtendedAttribute, nb []byte, err error) {
	var info fileFullEaInformation
	err = binary.Read(bytes.NewReader(b), binary.LittleEndian, &info)
	if err != nil {
		err = errInvalidEaBuffer
		return ea, nb, err
	}

	nameOffset := fileFullEaInformationSize
	nameLen := int(info.NameLength)
	valueOffset := nameOffset + int(info.NameLength) + 1
	valueLen := int(info.ValueLength)
	nextOffset := int(info.NextEntryOffset)
	if valueLen+valueOffset > len(b) || nextOffset < 0 || nextOffset > len(b) {
		err = errInvalidEaBuffer
		return ea, nb, err
	}

	ea.Name = string(b[nameOffset : nameOffset+nameLen])
	ea.Value = b[valueOffset : valueOffset+valueLen]
	ea.Flags = info.Flags
	if info.NextEntryOffset != 0 {
		nb = b[info.NextEntryOffset:]
	}
	return ea, nb, err
}

// DecodeExtendedAttributes decodes a list of EAs from a FILE_FULL_EA_INFORMATION
// buffer retrieved from BackupRead, ZwQueryEaFile, etc.
func DecodeExtendedAttributes(b []byte) (eas []ExtendedAttribute, err error) {
	for len(b) != 0 {
		ea, nb, err := parseEa(b)
		if err != nil {
			return nil, err
		}

		eas = append(eas, ea)
		b = nb
	}
	return eas, err
}

func writeEa(buf *bytes.Buffer, ea *ExtendedAttribute, last bool) error {
	if int(uint8(len(ea.Name))) != len(ea.Name) {
		return errEaNameTooLarge
	}
	if int(uint16(len(ea.Value))) != len(ea.Value) {
		return errEaValueTooLarge
	}
	entrySize := uint32(fileFullEaInformationSize + len(ea.Name) + 1 + len(ea.Value))
	withPadding := (entrySize + 3) &^ 3
	nextOffset := uint32(0)
	if !last {
		nextOffset = withPadding
	}
	info := fileFullEaInformation{
		NextEntryOffset: nextOffset,
		Flags:           ea.Flags,
		NameLength:      uint8(len(ea.Name)),
		ValueLength:     uint16(len(ea.Value)),
	}

	err := binary.Write(buf, binary.LittleEndian, &info)
	if err != nil {
		return err
	}

	_, err = buf.Write([]byte(ea.Name))
	if err != nil {
		return err
	}

	err = buf.WriteByte(0)
	if err != nil {
		return err
	}

	_, err = buf.Write(ea.Value)
	if err != nil {
		return err
	}

	_, err = buf.Write([]byte{0, 0, 0}[0 : withPadding-entrySize])
	if err != nil {
		return err
	}

	return nil
}

// EncodeExtendedAttributes encodes a list of EAs into a FILE_FULL_EA_INFORMATION
// buffer for use with BackupWrite, ZwSetEaFile, etc.
func EncodeExtendedAttributes(eas []ExtendedAttribute) ([]byte, error) {
	var buf bytes.Buffer
	for i := range eas {
		last := false
		if i == len(eas)-1 {
			last = true
		}

		err := writeEa(&buf, &eas[i], last)
		if err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func (status ntStatus) Err() error {
	if status >= 0 {
		return nil
	}
	return rtlNtStatusToDosError(status)
}

func rtlNtStatusToDosError(status ntStatus) (winerr error) {
	r0, _, _ := syscall.SyscallN(procRtlNtStatusToDosErrorNoTeb.Addr(), uintptr(status), 0, 0)
	if r0 != 0 {
		winerr = syscall.Errno(r0)
	}
	return
}
