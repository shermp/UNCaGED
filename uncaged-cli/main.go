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
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "image/jpeg"

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
	metadata     cliMeta
	deviceInfo   uc.DeviceInfo
}

type cliMeta struct {
	indices   []int
	currIndex int
	md        []uc.CalibreBookMeta
}

func (cm *cliMeta) reset() {
	cm.indices = make([]int, 0)
	cm.currIndex = 0
}
func (cm *cliMeta) addIndex(index int) {
	cm.indices = append(cm.indices, index)
}
func (cm *cliMeta) Count() int {
	return len(cm.indices)
}
func (cm *cliMeta) Next() bool {
	if len(cm.indices) == 0 {
		return false
	}
	cm.currIndex = cm.indices[0]
	cm.indices = cm.indices[1:]
	return true
}
func (cm *cliMeta) Get() (uc.CalibreBookMeta, error) {
	md := cm.md[cm.currIndex]
	if md.Cover != nil {
		thumbPath := *md.Cover
		img, err := ioutil.ReadFile(thumbPath)
		if err == nil {
			cfg, _, _ := image.DecodeConfig(bytes.NewReader(img))
			md.Thumbnail.Set(cfg.Width, cfg.Height, base64.StdEncoding.EncodeToString(img))
		}
	}
	return md, nil
}

func (cli *UncagedCLI) loadMDfile() error {
	mdJSON, err := ioutil.ReadFile(cli.metadataFile)
	if err != nil {
		cli.metadata.md = nil
		if os.IsNotExist(err) {
			emptyJSON := []byte("[]\n")
			ioutil.WriteFile(cli.metadataFile, emptyJSON, 0644)
		}
		return err
	}
	if len(mdJSON) == 0 {
		cli.metadata.md = nil
		return nil
	}
	return json.Unmarshal(mdJSON, &cli.metadata.md)
}

func (cli *UncagedCLI) saveMDfile() error {
	mdJSON, err := json.MarshalIndent(cli.metadata.md, "", "    ")
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

// SelectCalibreInstance allows the client to choose a calibre instance if multiple
// are found on the network
// The function should return the instance to use
func (cli *UncagedCLI) SelectCalibreInstance(calInstances []uc.CalInstance) uc.CalInstance {
	fmt.Println("The following Calibre instances were found:")
	for i, instance := range calInstances {
		fmt.Printf("\t%d. %s at %s\n", i, instance.Description, instance.Addr)
	}
	fmt.Println("Automatically selecting the first Calibre instance...")
	return calInstances[0]
}

// GetClientOptions returns all the client specific options required for UNCaGED
func (cli *UncagedCLI) GetClientOptions() (uc.ClientOptions, error) {
	var opts uc.ClientOptions
	opts.ClientName = "UNCaGED"
	opts.CoverDims.Height = 530
	opts.CoverDims.Width = 530
	opts.SupportedExt = []string{"epub", "mobi"}
	opts.DeviceName = cli.deviceName
	opts.DeviceModel = cli.deviceModel
	return opts, nil
}

// GetDeviceBookList returns a slice of all the books currently on the device
// A nil slice is interpreted has having no books on the device
func (cli *UncagedCLI) GetDeviceBookList() ([]uc.BookCountDetails, error) {
	mdLen := len(cli.metadata.md)
	if mdLen == 0 {
		return nil, nil
	}
	bookDet := make([]uc.BookCountDetails, mdLen)
	for i, md := range cli.metadata.md {
		lastMod := time.Now()
		if md.LastModified != nil {
			lastMod = *md.LastModified
		}
		pathComp := strings.Split(md.Lpath, ".")
		ext := "."
		if len(pathComp) > 1 {
			ext += pathComp[len(pathComp)-1]
		}
		bd := uc.BookCountDetails{
			UUID:         md.UUID,
			Lpath:        md.Lpath,
			LastModified: lastMod,
			Extension:    ext,
		}
		bookDet[i] = bd
	}
	return bookDet, nil
}

// GetMetadataIter creates an iterator that sends complete metadata for the books
// listed in lpaths, or for all books on device if lpaths is empty
func (cli *UncagedCLI) GetMetadataIter(books []uc.BookID) uc.MetadataIter {
	cli.metadata.reset()
	if len(books) == 0 {
		for i := range cli.metadata.md {
			cli.metadata.addIndex(i)
		}
		return &cli.metadata
	}
	for _, bk := range books {
		for i, md := range cli.metadata.md {
			if bk.Lpath == md.Lpath {
				cli.metadata.addIndex(i)
			}
		}
	}
	return &cli.metadata
}

// GetDeviceInfo asks the client for information about the drive info to use
func (cli *UncagedCLI) GetDeviceInfo() (uc.DeviceInfo, error) {
	return cli.deviceInfo, nil
}

// SetDeviceInfo sets the new device info, as comes from calibre. Only the nested
// struct DevInfo is modified.
func (cli *UncagedCLI) SetDeviceInfo(devInfo uc.DeviceInfo) error {
	cli.deviceInfo = devInfo
	cli.saveDriveInfoFile()
	return nil
}

// UpdateMetadata instructs the client to update their metadata according to the
// new slice of metadata maps
func (cli *UncagedCLI) UpdateMetadata(mdList []uc.CalibreBookMeta) error {
	// This is ugly. Is there a better way to do it?
	for _, newMD := range mdList {
		newMDlpath := newMD.Lpath
		newMDuuid := newMD.UUID
		for j, md := range cli.metadata.md {
			if newMDlpath == md.Lpath && newMDuuid == md.UUID {
				cli.metadata.md[j] = newMD
			}
		}
	}
	cli.saveMDfile()
	return nil
}

// GetPassword gets a password from the user.
func (cli *UncagedCLI) GetPassword(calibreInfo uc.CalibreInitInfo) (string, error) {
	// For testing purposes ONLY
	return "uncaged", nil
}

// GetFreeSpace reports the amount of free storage space to Calibre
func (cli *UncagedCLI) GetFreeSpace() uint64 {
	// For testing purposes ONLY
	return 1024 * 1024 * 1024
}

// CheckLpath asks the client to verify a provided Lpath, and change it if required
// Return the original string if the Lpath does not need changing
func (cli *UncagedCLI) CheckLpath(lpath string) string {
	return lpath
}

// SaveBook saves a book with the provided metadata to the disk.
// Implementations return an io.WriteCloser for UNCaGED to write the ebook to
func (cli *UncagedCLI) SaveBook(md uc.CalibreBookMeta, book io.Reader, len int, lastBook bool) (err error) {
	err = nil
	bookExists := false
	lpath := md.Lpath
	bookPath := filepath.Join(cli.bookDir, lpath)
	imgPath := bookPath + ".jpg"
	dir, _ := filepath.Split(bookPath)
	os.MkdirAll(dir, 0777)
	bookFile, err := os.OpenFile(bookPath, os.O_WRONLY|os.O_CREATE, 0644)
	written, err := io.CopyN(bookFile, book, int64(len))
	if written != int64(len) {
		return errors.New("Number of bytes written different from expected")
	} else if err != nil {
		return err
	}
	if md.Thumbnail.Exists() {
		w, h := md.Thumbnail.Dimensions()
		fmt.Printf("Thumbnail Dims... W: %d, H: %d\n", w, h)
		img, _ := base64.StdEncoding.DecodeString(md.Thumbnail.ImgBase64())
		if err = ioutil.WriteFile(imgPath, img, 0644); err != nil {
			return fmt.Errorf("SaveBook: failed to write cover: %w", err)
		}
		md.Cover = &imgPath
		md.Thumbnail = nil
	}
	for i, m := range cli.metadata.md {
		currLpath := m.Lpath
		if currLpath == lpath {
			bookExists = true
			cli.metadata.md[i] = md
		}
	}
	if !bookExists {
		cli.metadata.md = append(cli.metadata.md, md)
	}
	if lastBook {
		cli.saveMDfile()
	}
	return err
}

// GetBook provides an io.ReadCloser, from which UNCaGED can send the requested book to Calibre
func (cli *UncagedCLI) GetBook(book uc.BookID, filePos int64) (io.ReadCloser, int64, error) {
	bkPath := filepath.Join(cli.bookDir, book.Lpath)
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
func (cli *UncagedCLI) DeleteBook(book uc.BookID) error {
	bkPath := filepath.Join(cli.bookDir, book.Lpath)
	//dir, _ := filepath.Split(bkPath)
	err := os.Remove(bkPath)
	if err != nil {
		return err
	}
	for i, md := range cli.metadata.md {
		if md.Lpath == book.Lpath {
			cli.metadata.md[i] = cli.metadata.md[len(cli.metadata.md)-1]
			cli.metadata.md[len(cli.metadata.md)-1] = uc.CalibreBookMeta{}
			cli.metadata.md = cli.metadata.md[:len(cli.metadata.md)-1]
			break
		}
	}
	cli.saveMDfile()
	return nil
}
func (cli *UncagedCLI) UpdateStatus(status uc.Status, progress int) {

}

// LogPrintf instructs the client to log stuff
func (cli *UncagedCLI) LogPrintf(logLevel uc.LogLevel, format string, a ...interface{}) {
	fmt.Printf(format, a...)
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
	uc, err := uc.New(cli, true)
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
