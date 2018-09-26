/*
	UNCaGED - Universal Networked Calibre Go Ereader Device
    Copyright (C) 2018 Sherman Perry

    This file is part of UNCaGED.

    UNCaGED is free software: you can redistribute it and/or modify
    it under the terms of the GNU General Public License as published by
    the Free Software Foundation, either version 3 of the License, or
    (at your option) any later version.

    UNCaGED is distributed in the hope that it will be useful,
    but WITHOUT ANY WARRANTY; without even the implied warranty of
    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
    GNU General Public License for more details.

    You should have received a copy of the GNU General Public License
    along with UNCaGED.  If not, see <https://www.gnu.org/licenses/>.
*/

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shermp/UNCaGED/uc"
)

const metadataFile = ".metadata.calibre"
const drivinfoFile = ".driveinfo.calibre"

type UncagedCLI struct {
	deviceName   string
	deviceModel  string
	bookDir      string
	metadataFile string
	drivinfoFile string
	metadata     []map[string]interface{}
	deviceInfo   uc.DeviceInfo
}

func (cli *UncagedCLI) loadMDfile() error {
	mdJSON, err := ioutil.ReadFile(cli.metadataFile)
	if err != nil {
		cli.metadata = nil
		if os.IsNotExist(err) {
			emptyJSON := []byte("[]\n")
			ioutil.WriteFile(cli.metadataFile, emptyJSON, 0644)
		}
		return err
	}
	if len(mdJSON) == 0 {
		cli.metadata = nil
		return nil
	}
	return json.Unmarshal(mdJSON, &cli.metadata)
}

func (cli *UncagedCLI) saveMDfile() error {
	mdJSON, err := json.MarshalIndent(cli.metadata, "", "    ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(cli.metadataFile, mdJSON, 0644)
}

func (cli *UncagedCLI) loadDriveInfoFile() error {
	diJSON, err := ioutil.ReadFile(cli.drivinfoFile)
	if err != nil {
		if err == os.ErrNotExist {
			emptyJSON := []byte("[]\n")
			ioutil.WriteFile(cli.drivinfoFile, emptyJSON, 0644)
		}
		return err
	}
	if len(diJSON) > 0 {
		err = json.Unmarshal(diJSON, &cli.deviceInfo.DevInfo)
	}
	return err
}

func (cli *UncagedCLI) saveDriveInfoFile() error {
	diJSON, err := json.MarshalIndent(cli.deviceInfo.DevInfo, "", "    ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(cli.drivinfoFile, diJSON, 0644)
}

// GetClientOptions returns all the client specific options required for UNCaGED
func (cli *UncagedCLI) GetClientOptions() uc.ClientOptions {
	var opts uc.ClientOptions
	opts.ClientName = "UNCaGED"
	opts.CoverDims.Height = 530
	opts.CoverDims.Width = 530
	opts.SupportedExt = []string{"epub", "mobi"}
	opts.DeviceName = cli.deviceName
	opts.DeviceModel = cli.deviceModel
	return opts
}

// GetDeviceBookList returns a slice of all the books currently on the device
// A nil slice is interpreted has having no books on the device
func (cli *UncagedCLI) GetDeviceBookList() []uc.BookCountDetails {
	mdLen := len(cli.metadata)
	if mdLen == 0 {
		return nil
	}
	bookDet := make([]uc.BookCountDetails, mdLen)
	for i, md := range cli.metadata {
		lastMod, _ := time.Parse(time.RFC3339, md["last_modified"].(string))
		pathComp := strings.Split(md["lpath"].(string), ".")
		ext := "."
		if len(pathComp) > 1 {
			ext += pathComp[len(pathComp)-1]
		}
		bd := uc.BookCountDetails{
			UUID:         md["uuid"].(string),
			Lpath:        md["lpath"].(string),
			LastModified: lastMod,
			Extension:    ext,
		}
		bookDet[i] = bd
	}
	return bookDet
}

// GetDeviceInfo asks the client for information about the drive info to use
func (cli *UncagedCLI) GetDeviceInfo() uc.DeviceInfo {
	return cli.deviceInfo
}

// SetDeviceInfo sets the new device info, as comes from calibre. Only the nested
// struct DevInfo is modified.
func (cli *UncagedCLI) SetDeviceInfo(devinfo uc.DeviceInfo) {
	cli.deviceInfo = devinfo
	cli.saveDriveInfoFile()
}

// UpdateMetadata instructs the client to update their metadata according to the
// new slice of metadata maps
func (cli *UncagedCLI) UpdateMetadata(mdList []map[string]interface{}) {
	// This is ugly. Is there a better way to do it?
	for _, newMD := range mdList {
		newMDlpath := newMD["lpath"].(string)
		newMDuuid := newMD["uuid"].(string)
		for j, md := range cli.metadata {
			if newMDlpath == md["lpath"].(string) && newMDuuid == md["uuid"].(string) {
				cli.metadata[j] = newMD
			}
		}
	}
	cli.saveMDfile()
}

// GetPassword gets a password from the user.
func (cli *UncagedCLI) GetPassword() string {
	// For testing purposes ONLY
	return "testpass"
}

// GetFreeSpace reports the amount of free storage space to Calibre
func (cli *UncagedCLI) GetFreeSpace() uint64 {
	// For testing purposes ONLY
	return 1024 * 1024 * 1024
}

// SaveBook saves a book with the provided metadata to the disk.
// Implementations return an io.WriteCloser for UNCaGED to write the ebook to
func (cli *UncagedCLI) SaveBook(md map[string]interface{}, lastBook bool) (io.WriteCloser, error) {
	bookExists := false
	lpath := md["lpath"].(string)
	bookPath := filepath.Join(cli.bookDir, lpath)
	dir, _ := filepath.Split(bookPath)
	os.MkdirAll(dir, 0777)
	bookFile, err := os.OpenFile(bookPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	for i, m := range cli.metadata {
		currLpath := m["lpath"].(string)
		if currLpath == lpath {
			bookExists = true
			cli.metadata[i] = md
		}
	}
	if !bookExists {
		cli.metadata = append(cli.metadata, md)
	}
	if lastBook {
		cli.saveMDfile()
	}
	return bookFile, nil
}

// GetBook provides an io.ReadCloser, from which UNCaGED can send the requested book to Calibre
func (cli *UncagedCLI) GetBook(lpath, uuid string, filePos int64) (io.ReadCloser, int64, error) {
	bkPath := filepath.Join(cli.bookDir, lpath)
	bkFile, err := os.OpenFile(bkPath, os.O_RDONLY, 0644)
	if err != nil {
		return nil, -1, err
	}
	fi, err := bkFile.Stat()
	if err != nil {
		return nil, -1, err
	}
	if filePos > 0 {
		bkFile.Seek(filePos, os.SEEK_SET)
	}
	return bkFile, fi.Size(), nil
}

// DeleteBook instructs the client to delete the specified book on the device
// Error is returned if the book was unable to be deleted
func (cli *UncagedCLI) DeleteBook(lpath, uuid string) error {
	bkPath := filepath.Join(cli.bookDir, lpath)
	//dir, _ := filepath.Split(bkPath)
	err := os.Remove(bkPath)
	if err != nil {
		return err
	}
	return nil
}

// Println is used to print messages to the users display. Usage is identical to
// that of fmt.Println()
func (cli *UncagedCLI) Println(a ...interface{}) (n int, err error) {
	return fmt.Println(a...)
}

// DisplayProgress Instructs the client to display the current progress to the user.
// percentage will be an integer between 0 and 100 inclusive
func (cli *UncagedCLI) DisplayProgress(percentage int) {
	fmt.Printf("Current Progress: %d%%", percentage)
	if percentage == 100 {
		fmt.Printf("\n")
	}
}

func main() {
	cwd, _ := os.Getwd()
	cli := &UncagedCLI{
		deviceName:   "UNCaGED",
		deviceModel:  "CLI",
		bookDir:      filepath.Join(cwd, "library/"),
		metadataFile: filepath.Join(cwd, "library/", metadataFile),
		drivinfoFile: filepath.Join(cwd, "library/", drivinfoFile),
	}
	err := os.MkdirAll(cli.bookDir, 0777)
	if err != nil {
		fmt.Println(err)
		return
	}
	err = cli.loadMDfile()
	if err != nil {
		fmt.Println(err)
	}
	err = cli.loadDriveInfoFile()
	if err != nil {
		fmt.Println(err)
	}
	if cli.deviceInfo.DevInfo.DeviceName == "" {
		cli.deviceInfo.DevInfo.DeviceName = cli.deviceName
		cli.deviceInfo.DevInfo.LocationCode = "main"
		cli.deviceInfo.DevInfo.DeviceStoreUUID = "586e12c6-50b7-43bf-be8d-a4a0b85be530"
	}
	uc, err := uc.New(cli)
	if err != nil {
		fmt.Println(err)
		return
	}
	err = uc.Start()
	if err != nil {
		fmt.Println(err)
		return
	}
}
