//go:build windows
// +build windows

package restic_test

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/test"
	"golang.org/x/sys/windows"
)

func TestRestoreExtendedAttributes(t *testing.T) {
	tempDir := t.TempDir()
	expectedNodes := []restic.Node{
		{
			Name:       "testfile",
			Type:       "file",
			Mode:       0644,
			ModTime:    parseTime("2005-05-14 21:07:03.111"),
			AccessTime: parseTime("2005-05-14 21:07:04.222"),
			ChangeTime: parseTime("2005-05-14 21:07:05.333"),
			ExtendedAttributes: []restic.ExtendedAttribute{
				{"user.foo", []byte("bar")},
			},
		},
		{
			Name:       "testdirectory",
			Type:       "dir",
			Mode:       0755,
			ModTime:    parseTime("2005-05-14 21:07:03.111"),
			AccessTime: parseTime("2005-05-14 21:07:04.222"),
			ChangeTime: parseTime("2005-05-14 21:07:05.333"),
			ExtendedAttributes: []restic.ExtendedAttribute{
				{"user.foo", []byte("bar")},
			},
		},
	}
	for _, testNode := range expectedNodes {
		testPath := filepath.Join(tempDir, "001", testNode.Name)
		if err := os.MkdirAll(filepath.Dir(testPath), testNode.Mode); err != nil {
			t.Fatalf("Failed to create parent directories: %v", err)
		}
		if testNode.Type == "file" {

			testFile, err := os.Create(testPath)
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}
			testFile.Close()
		} else if testNode.Type == "dir" {

			err := os.Mkdir(testPath, testNode.Mode)
			if err != nil {
				t.Fatalf("Failed to create test directory: %v", err)
			}
		}

		err := testNode.RestoreMetadata(testPath)
		if err != nil {
			t.Fatalf("Error restoring metadata: %v", err)
		}
		var handle windows.Handle
		utf16Path := windows.StringToUTF16Ptr(testPath)
		if testNode.Type == "file" {
			handle, err = windows.CreateFile(utf16Path, windows.FILE_READ_EA, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		} else if testNode.Type == "dir" {
			handle, err = windows.CreateFile(utf16Path, windows.FILE_READ_EA, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		}
		if err != nil {
			t.Fatalf("Error opening file/directory: %v", err)
		}
		defer func() {
			err := windows.Close(handle)
			if err != nil {
				t.Logf("Error closing file %s: %v\n", testPath, err)
			}
		}()

		if len(testNode.ExtendedAttributes) > 0 {
			extAttr, err := fs.GetFileEA(handle)
			if err != nil {
				t.Fatalf("Error getting extended attributes: %v", err)
			}
			test.Equals(t, len(testNode.ExtendedAttributes), len(extAttr))

			for _, expectedExtAttr := range testNode.ExtendedAttributes {
				var foundExtAttr *fs.ExtendedAttribute
				for _, ea := range extAttr {
					if strings.EqualFold(ea.Name, expectedExtAttr.Name) {
						foundExtAttr = &ea
						break

					}
				}
				test.Assert(t, foundExtAttr != nil, "Expected extended attribute not found")
				test.Equals(t, expectedExtAttr.Value, foundExtAttr.Value)
			}
		}
	}
}

func TestRestoreSecurityDescriptors(t *testing.T) {
	tempDir := t.TempDir()
	expectedNodes := []restic.Node{
		{
			Name:       "testfile",
			Type:       "file",
			Mode:       0644,
			ModTime:    parseTime("2005-05-14 21:07:03.111"),
			AccessTime: parseTime("2005-05-14 21:07:04.222"),
			ChangeTime: parseTime("2005-05-14 21:07:05.333"),
			ExtendedAttributes: []restic.ExtendedAttribute{
				{"user.foo", []byte("bar")},
			},
			GenericAttributes: []restic.GenericAttribute{
				restic.NewGenericAttribute(restic.TypeSecurityDescriptor, []byte("AQAUrBQAAAAwAAAAAAAAAEwAAAABBQAAAAAABRUAAACIn1iuVqCC6sy9JqvqAwAAAQUAAAAAAAUVAAAAiJ9YrlaggurMvSarAQIAAAIAfAAEAAAAAAAkAKkAEgABBQAAAAAABRUAAACIn1iuVqCC6sy9JqvtAwAAABAUAP8BHwABAQAAAAAABRIAAAAAEBgA/wEfAAECAAAAAAAFIAAAACACAAAAECQA/wEfAAEFAAAAAAAFFQAAAIifWK5WoILqzL0mq+oDAAA=")),
			},
		},
		{
			Name:       "testfile2",
			Type:       "file",
			Mode:       0644,
			ModTime:    parseTime("2005-05-14 21:07:03.111"),
			AccessTime: parseTime("2005-05-14 21:07:04.222"),
			ChangeTime: parseTime("2005-05-14 21:07:05.333"),
			ExtendedAttributes: []restic.ExtendedAttribute{
				{"user.foo2", []byte("bar2")},
			},
			GenericAttributes: []restic.GenericAttribute{
				restic.NewGenericAttribute(restic.TypeSecurityDescriptor, []byte("AQAUjBQAAAAwAAAA7AAAAEwAAAABBQAAAAAABRUAAAAvr7t03PyHGk2FokNHCAAAAQUAAAAAAAUVAAAAiJ9YrlaggurMvSarAQIAAAIAoAAFAAAAAAAkAP8BHwABBQAAAAAABRUAAAAvr7t03PyHGk2FokNHCAAAAAAkAKkAEgABBQAAAAAABRUAAACIn1iuVqCC6sy9JqvtAwAAABAUAP8BHwABAQAAAAAABRIAAAAAEBgA/wEfAAECAAAAAAAFIAAAACACAAAAECQA/wEfAAEFAAAAAAAFFQAAAIifWK5WoILqzL0mq+oDAAACAHQAAwAAAAKAJAC/AQIAAQUAAAAAAAUVAAAAL6+7dNz8hxpNhaJDtgQAAALAJAC/AQMAAQUAAAAAAAUVAAAAL6+7dNz8hxpNhaJDPgkAAAJAJAD/AQ8AAQUAAAAAAAUVAAAAL6+7dNz8hxpNhaJDtQQAAA==")),
			},
		},
		{
			Name:       "testdirectory",
			Type:       "dir",
			Mode:       0755,
			ModTime:    parseTime("2005-05-14 21:07:03.111"),
			AccessTime: parseTime("2005-05-14 21:07:04.222"),
			ChangeTime: parseTime("2005-05-14 21:07:05.333"),
			ExtendedAttributes: []restic.ExtendedAttribute{
				{"user.foo3", []byte("bar3")},
			},
			GenericAttributes: []restic.GenericAttribute{
				restic.NewGenericAttribute(restic.TypeSecurityDescriptor, []byte("AQAUrBQAAAAwAAAAAAAAAEwAAAABBQAAAAAABRUAAACIn1iuVqCC6sy9JqvqAwAAAQUAAAAAAAUVAAAAiJ9YrlaggurMvSarAQIAAAIAfAAEAAAAAAAkAKkAEgABBQAAAAAABRUAAACIn1iuVqCC6sy9JqvtAwAAABMUAP8BHwABAQAAAAAABRIAAAAAExgA/wEfAAECAAAAAAAFIAAAACACAAAAEyQA/wEfAAEFAAAAAAAFFQAAAIifWK5WoILqzL0mq+oDAAA=")),
			},
		},
		{
			Name:       "testdirectory2",
			Type:       "dir",
			Mode:       0755,
			ModTime:    parseTime("2005-05-14 21:07:03.111"),
			AccessTime: parseTime("2005-05-14 21:07:04.222"),
			ChangeTime: parseTime("2005-05-14 21:07:05.333"),
			ExtendedAttributes: []restic.ExtendedAttribute{
				{"user.foo4", []byte("bar4")},
			},
			GenericAttributes: []restic.GenericAttribute{
				restic.NewGenericAttribute(restic.TypeSecurityDescriptor, []byte("AQAUrBQAAAAwAAAAAAAAAEwAAAABBQAAAAAABRUAAACIn1iuVqCC6sy9JqvqAwAAAQUAAAAAAAUVAAAAiJ9YrlaggurMvSarAQIAAAIA3AAIAAAAAAIUAKkAEgABAQAAAAAABQcAAAAAAxQAiQASAAEBAAAAAAAFBwAAAAAAJACpABIAAQUAAAAAAAUVAAAAiJ9YrlaggurMvSar7QMAAAAAJAC/ARMAAQUAAAAAAAUVAAAAiJ9YrlaggurMvSar6gMAAAALFAC/ARMAAQEAAAAAAAMAAAAAABMUAP8BHwABAQAAAAAABRIAAAAAExgA/wEfAAECAAAAAAAFIAAAACACAAAAEyQA/wEfAAEFAAAAAAAFFQAAAIifWK5WoILqzL0mq+oDAAA=")),
			},
		},
	}
	for _, testNode := range expectedNodes {
		testPath := filepath.Join(tempDir, "001", testNode.Name)
		if err := os.MkdirAll(filepath.Dir(testPath), testNode.Mode); err != nil {
			t.Fatalf("Failed to create parent directories: %v", err)
		}
		if testNode.Type == "file" {

			testFile, err := os.Create(testPath)
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}
			testFile.Close()
		} else if testNode.Type == "dir" {

			err := os.Mkdir(testPath, testNode.Mode)
			if err != nil {
				t.Fatalf("Failed to create test directory: %v", err)
			}
		}

		err := testNode.RestoreMetadata(testPath)
		if err != nil {
			t.Fatalf("Error restoring metadata: %v", err)
		}
		var handle windows.Handle
		utf16Path := windows.StringToUTF16Ptr(testPath)
		if testNode.Type == "file" {
			handle, err = windows.CreateFile(utf16Path, windows.FILE_READ_EA, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		} else if testNode.Type == "dir" {
			handle, err = windows.CreateFile(utf16Path, windows.FILE_READ_EA, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		}
		if err != nil {
			t.Fatalf("Error opening file/directory: %v", err)
		}
		defer func() {
			err := windows.Close(handle)
			if err != nil {
				t.Logf("Error closing file %s: %v\n", testPath, err)
			}
		}()

		extAttr, err := fs.GetFileEA(handle)
		if err != nil {
			t.Fatalf("Error getting extended attributes: %v", err)
		}

		test.Equals(t, len(testNode.ExtendedAttributes), len(extAttr), "Unexpected count of extended attribs for path: %s", testPath)

		for _, expectedExtAttr := range testNode.ExtendedAttributes {
			var foundExtAttr *fs.ExtendedAttribute
			for _, ea := range extAttr {
				if strings.EqualFold(ea.Name, expectedExtAttr.Name) {
					foundExtAttr = &ea
					break
				}
			}
			test.Assert(t, foundExtAttr != nil, "Expected extended attribute not found")
			test.Equals(t, expectedExtAttr.Value, foundExtAttr.Value, "Expected attribute value not equal for: %s", expectedExtAttr.Name)
		}

		sd, err := fs.GetFileSecurityDescriptor(testPath)

		test.Assert(t, err == nil, "Error while getting the security descriptor")

		testSD := string(testNode.GetGenericAttribute(restic.TypeSecurityDescriptor))
		sdBytesTest, err := base64.StdEncoding.DecodeString(testSD)
		if err != nil {
			t.Fatalf("Error decoding SD: %s", err)
		}
		sdInput, err := fs.SecurityDescriptorBytesToStruct(sdBytesTest)

		if err != nil {
			t.Fatalf("Error converting SD to struct: %s", err)
		}

		sdBytesOutput, err := base64.StdEncoding.DecodeString(sd)
		if err != nil {
			t.Fatalf("Error decoding SD: %s", err)
		}
		sdOutput, err := fs.SecurityDescriptorBytesToStruct(sdBytesOutput)
		if err != nil {
			t.Fatalf("Error converting Output SD to struct: %s", err)
		}

		test.Equals(t, sdInput, sdOutput, "SecurityDescriptors not equal for path: %s", testPath)

		fi, err := os.Lstat(testPath)
		if err != nil {
			t.Fatal(err)
		}

		nodeFromFileInfo, err := restic.NodeFromFileInfo(testPath, fi)
		if err != nil {
			t.Fatal(err)
		}

		sdNodeFromFileInfoInput := sdOutput

		sdBytesFromNode := nodeFromFileInfo.GetGenericAttribute(restic.TypeSecurityDescriptor)

		sdByteNodeOutput, err := base64.StdEncoding.DecodeString(string(sdBytesFromNode))
		if err != nil {
			t.Fatalf("Error decoding SD: %s", err)
		}
		sdNodeFromFileInfoOutput, err := fs.SecurityDescriptorBytesToStruct(sdByteNodeOutput)
		if err != nil {
			t.Fatalf("Error converting Output SD Through Node to struct: %s", err)
		}

		test.Equals(t, sdNodeFromFileInfoInput, sdNodeFromFileInfoOutput, "SecurityDescriptors got from NodeFromFileInfo not equal for path: %s", testPath)
	}
}

func TestRestoreCreationTime(t *testing.T) {
	tempDir := t.TempDir()
	//Using the temp dir creation time as the test creation time for the test file and folder
	creationTime := getCreationTime(t, tempDir)
	runGenericAttributesTest(tempDir, t, restic.TypeCreationTime, creationTime)
}

func TestRestoreFileAttributes(t *testing.T) {
	genericAttributeName := restic.TypeFileAttribute
	tempDir := t.TempDir()
	fileAttributes := [][]byte{
		//normal
		[]uint8{syscall.FILE_ATTRIBUTE_NORMAL, 0x0, 0x0, 0x0},
		//hidden
		[]uint8{syscall.FILE_ATTRIBUTE_HIDDEN, 0x0, 0x0, 0x0},
		//system
		[]uint8{syscall.FILE_ATTRIBUTE_SYSTEM, 0x0, 0x0, 0x0},
		//archive
		[]uint8{syscall.FILE_ATTRIBUTE_ARCHIVE, 0x0, 0x0, 0x0},
	}
	for i, fileAttr := range fileAttributes {
		expectedNodes := []restic.Node{
			{
				Name:              fmt.Sprintf("testfile%d", i),
				Type:              "file",
				Mode:              0655,
				ModTime:           parseTime("2005-05-14 21:07:03.111"),
				AccessTime:        parseTime("2005-05-14 21:07:04.222"),
				ChangeTime:        parseTime("2005-05-14 21:07:05.333"),
				GenericAttributes: []restic.GenericAttribute{restic.NewGenericAttribute(genericAttributeName, fileAttr)},
			},
		}
		runGenericAttributesTestForNodes(expectedNodes, tempDir, t, genericAttributeName, fileAttr)
	}

	folderAttributes := [][]byte{
		//hidden
		[]uint8{syscall.FILE_ATTRIBUTE_DIRECTORY | syscall.FILE_ATTRIBUTE_HIDDEN, 0, 0, 0},
		//normal
		[]uint8{syscall.FILE_ATTRIBUTE_DIRECTORY, 0, 0, 0},
	}
	for i, folderAttr := range folderAttributes {
		expectedNodes := []restic.Node{
			{
				Name:              fmt.Sprintf("testdirectory%d", i),
				Type:              "dir",
				Mode:              0755,
				ModTime:           parseTime("2005-05-14 21:07:03.111"),
				AccessTime:        parseTime("2005-05-14 21:07:04.222"),
				ChangeTime:        parseTime("2005-05-14 21:07:05.333"),
				GenericAttributes: []restic.GenericAttribute{restic.NewGenericAttribute(genericAttributeName, folderAttr)},
			},
		}
		runGenericAttributesTestForNodes(expectedNodes, tempDir, t, genericAttributeName, folderAttr)
	}
}

func runGenericAttributesTest(tempDir string, t *testing.T, genericAttributeName restic.GenericAttributeType, genericAttributeExpected []byte) {
	expectedNodes := []restic.Node{
		{
			Name:              "testfile",
			Type:              "file",
			Mode:              0644,
			ModTime:           parseTime("2005-05-14 21:07:03.111"),
			AccessTime:        parseTime("2005-05-14 21:07:04.222"),
			ChangeTime:        parseTime("2005-05-14 21:07:05.333"),
			GenericAttributes: []restic.GenericAttribute{restic.NewGenericAttribute(genericAttributeName, genericAttributeExpected)},
		},
		{
			Name:              "testdirectory",
			Type:              "dir",
			Mode:              0755,
			ModTime:           parseTime("2005-05-14 21:07:03.111"),
			AccessTime:        parseTime("2005-05-14 21:07:04.222"),
			ChangeTime:        parseTime("2005-05-14 21:07:05.333"),
			GenericAttributes: []restic.GenericAttribute{restic.NewGenericAttribute(genericAttributeName, genericAttributeExpected)},
		},
	}
	runGenericAttributesTestForNodes(expectedNodes, tempDir, t, genericAttributeName, genericAttributeExpected)
}
func runGenericAttributesTestForNodes(expectedNodes []restic.Node, tempDir string, t *testing.T, genericAttr restic.GenericAttributeType, genericAttributeExpected []byte) {

	for _, testNode := range expectedNodes {
		testPath, genericAttrThroughNodeFromFileInfo := prepareGenericAttributesTestForNodes(tempDir, testNode, t, genericAttr)
		test.Equals(t, genericAttributeExpected, genericAttrThroughNodeFromFileInfo, "Generic attribute: %s got from NodeFromFileInfo not equal for path: %s", string(genericAttr), testPath)
	}
}

func prepareGenericAttributesTestForNodes(tempDir string, testNode restic.Node, t *testing.T, genericAttr restic.GenericAttributeType) (string, []byte) {
	testPath := filepath.Join(tempDir, "001", testNode.Name)
	if err := os.MkdirAll(filepath.Dir(testPath), testNode.Mode); err != nil {
		t.Fatalf("Failed to create parent directories: %v", err)
	}
	if testNode.Type == "file" {

		testFile, err := os.Create(testPath)
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		testFile.Close()
	} else if testNode.Type == "dir" {

		err := os.Mkdir(testPath, testNode.Mode)
		if err != nil {
			t.Fatalf("Failed to create test directory: %v", err)
		}
	}

	err := testNode.RestoreMetadata(testPath)
	if err != nil {
		t.Fatalf("Error restoring metadata: %v", err)
	}

	fi, err := os.Lstat(testPath)
	if err != nil {
		t.Fatal(errors.Wrapf(err, "Could not Lstat for path: %s", testPath))
	}

	nodeFromFileInfo, err := restic.NodeFromFileInfo(testPath, fi)
	if err != nil {
		t.Fatal(errors.Wrapf(err, "Could not get NodeFromFileInfo for path: %s", testPath))
	}
	genericAttrThroughNodeFromFileInfo := nodeFromFileInfo.GetGenericAttribute(genericAttr)
	return testPath, genericAttrThroughNodeFromFileInfo
}

func getCreationTime(t *testing.T, path string) []byte {
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(errors.Wrapf(err, "Could not Lstat for path: %s", path))
	}

	attrib, success := fi.Sys().(*syscall.Win32FileAttributeData)
	if success && attrib != nil {
		var creationTime [8]byte
		binary.LittleEndian.PutUint32(creationTime[0:4], attrib.CreationTime.LowDateTime)
		binary.LittleEndian.PutUint32(creationTime[4:8], attrib.CreationTime.HighDateTime)
		return creationTime[:]
	} else {
		t.Fatal("Could not get creation time for path: " + path)
	}
	return nil
}

const TypeSomeNewAttribute restic.GenericAttributeType = "someNewAttribute"

func TestNewGenericAttributeType(t *testing.T) {
	genericAttributeName := TypeSomeNewAttribute
	tempDir := t.TempDir()
	expectedNodes := []restic.Node{
		{
			Name:              "testfile",
			Type:              "file",
			Mode:              0644,
			ModTime:           parseTime("2005-05-14 21:07:03.111"),
			AccessTime:        parseTime("2005-05-14 21:07:04.222"),
			ChangeTime:        parseTime("2005-05-14 21:07:05.333"),
			GenericAttributes: []restic.GenericAttribute{restic.NewGenericAttribute(genericAttributeName, []byte("any value"))},
		},
		{
			Name:              "testdirectory",
			Type:              "dir",
			Mode:              0755,
			ModTime:           parseTime("2005-05-14 21:07:03.111"),
			AccessTime:        parseTime("2005-05-14 21:07:04.222"),
			ChangeTime:        parseTime("2005-05-14 21:07:05.333"),
			GenericAttributes: []restic.GenericAttribute{restic.NewGenericAttribute(genericAttributeName, []byte("any value"))},
		},
	}
	for _, testNode := range expectedNodes {
		testPath, genericAttrThroughNodeFromFileInfo := prepareGenericAttributesTestForNodes(tempDir, testNode, t, TypeSomeNewAttribute)
		//Since this GenericAttribute is unknown to this version of the software, it will not get set on the file.
		test.Equals(t, []byte(nil), genericAttrThroughNodeFromFileInfo, "Unknown Generic attribute: %s got from NodeFromFileInfo not equal for path: %s", string(TypeSomeNewAttribute), testPath)
	}
}
