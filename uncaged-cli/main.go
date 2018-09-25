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
	"fmt"
	"io"

	"github.com/shermp/UNCaGED/uc"
)

type UncagedCLI struct {
	deviceName  string
	deviceModel string
	bookDir     string
}

// GetClientOptions returns all the client specific options required for UNCaGED
func (cli *UncagedCLI) GetClientOptions() uc.ClientOptions {

}

// GetDeviceBookList returns a slice of all the books currently on the device
// A nil slice is interpreted has having no books on the device
func (cli *UncagedCLI) GetDeviceBookList() []uc.BookCountDetails {

}

// GetDeviceInfo asks the client for information about the drive info to use
func (cli *UncagedCLI) GetDeviceInfo() uc.DeviceInfo {

}

// SetDeviceInfo sets the new device info, as comes from calibre. Only the nested
// struct DevInfo is modified.
func (cli *UncagedCLI) SetDeviceInfo(uc.DeviceInfo) {

}

// GetPassword gets a password from the user.
func (cli *UncagedCLI) GetPassword() string {

}

// GetFreeSpace reports the amount of free storage space to Calibre
func (cli *UncagedCLI) GetFreeSpace() uint64 {

}

// SaveBook saves a book with the provided metadata to the disk.
// Implementations return an io.WriteCloser for UNCaGED to write the ebook to
func (cli *UncagedCLI) SaveBook(md map[string]interface{}) (io.WriteCloser, error) {

}

// GetBook provides an io.ReadCloser, from which UNCaGED can send the requested book to Calibre
func (cli *UncagedCLI) GetBook(lpath, uuid string) (io.ReadCloser, error) {

}

// DeleteBook instructs the client to delete the specified book on the device
// Error is returned if the book was unable to be deleted
func (cli *UncagedCLI) DeleteBook(lpath, uuid string) error {

}

// Println is used to print messages to the users display. Usage is identical to
// that of fmt.Println()
func (cli *UncagedCLI) Println(a ...interface{}) (n int, err error) {
	return fmt.Println(a...)
}

// DisplayProgress Instructs the client to display the current progress to the user.
// percentage will be an integer between 0 and 100 inclusive
func (cli *UncagedCLI) DisplayProgress(percentage int) {

}

func main() {

}
