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

package uc

import (
	"bufio"
	"io"
	"net"
	"time"
)

type calOpCode int
type calMsgCode int
type ucdbSearchType int
type LogLevel int
type Status int

// Calibre opcodes
const (
	noop                  calOpCode = 12
	ok                    calOpCode = 0
	bookDone              calOpCode = 11
	calibreBusy           calOpCode = 18
	setLibraryInfo        calOpCode = 19
	deleteBook            calOpCode = 13
	displayMessage        calOpCode = 17
	freeSpace             calOpCode = 5
	getBookFileSegment    calOpCode = 14
	getBookMetadata       calOpCode = 15
	getBookCount          calOpCode = 6
	getDeviceInformation  calOpCode = 3
	getInitializationInfo calOpCode = 9
	sendBooklists         calOpCode = 7
	sendBook              calOpCode = 8
	sendBookMetadata      calOpCode = 16
	setCalibreDeviceInfo  calOpCode = 1
	setCalibreDeviceName  calOpCode = 2
	totalSpace            calOpCode = 4
)

// Calibre essage codes
const (
	passwordError calMsgCode = 1
	updateNeeded  calMsgCode = 2
	showToast     calMsgCode = 3
)

// ucdb search types
const (
	PriKey ucdbSearchType = iota
	Lpath
)

// UNCaGED log levels
const (
	Info LogLevel = iota
	Warn
	Debug
)

// UNCaGED status indicators
const (
	SearchingCalibre Status = iota
	Connecting
	Connected
	Disconnected
	Idle
	ReceivingBook
	SendingBook
	SendingExtraMetadata
	EmptyPasswordReceived
)

// UncagedDB is the structure used by UNCaGED's internal database
type UncagedDB struct {
	nextKey  int
	booklist []BookCountDetails
}

// Client is the interface that specific implementations of UNCaGED must implement.
type Client interface {
	// GetClientOptions returns all the client specific options required for UNCaGED
	GetClientOptions() ClientOptions
	// GetDeviceBookList returns a slice of all the books currently on the device
	// A nil slice is interpreted has having no books on the device
	GetDeviceBookList() []BookCountDetails
	// GetMetadataList sends complete metadata for the books listed in lpaths, or for
	// all books on device if lpaths is empty
	GetMetadataList(books []BookID) []map[string]interface{}
	// GetDeviceInfo asks the client for information about the drive info to use
	GetDeviceInfo() DeviceInfo
	// SetDeviceInfo sets the new device info, as comes from calibre. Only the nested
	// struct DevInfo is modified.
	SetDeviceInfo(devInfo DeviceInfo)
	// UpdateMetadata instructs the client to update their metadata according to the
	// new slice of metadata maps
	UpdateMetadata(mdList []map[string]interface{})
	// GetPassword gets a password from the user.
	GetPassword(calibreInfo CalibreInitInfo) string
	// GetFreeSpace reports the amount of free storage space to Calibre
	GetFreeSpace() uint64
	// SaveBook saves a book with the provided metadata to the disk.
	// Implementations return an io.WriteCloser (book) for UNCaGED to write the ebook to
	// len informs the client of the expected book size that will be written
	// lastBook informs the client that this is the last book for this transfer
	// newLpath informs UNCaGED of an Lpath change. Use this if the lpath field in md is
	// not valid (eg filesystem limitations.). Return an empty string if original lpath is valid
	SaveBook(md map[string]interface{}, len int, lastBook bool) (book io.WriteCloser, newLpath string, err error)
	// GetBook provides an io.ReadCloser, and the file len, from which UNCaGED can send the requested book to Calibre
	// NOTE: filePos > 0 is not currently implemented in the Calibre source code, but that could
	// change at any time, so best to handle it anyway.
	GetBook(book BookID, filePos int64) (io.ReadCloser, int64, error)
	// DeleteBook instructs the client to delete the specified book on the device
	// Error is returned if the book was unable to be deleted
	DeleteBook(book BookID) error
	// UpdateStatus informs the client what UNCaGED is doing. It is purely informational,
	// and it's implementation may be empty
	// status: What UC is currently doing (eg: receiving book(s))
	// progress: If the current status has a progress associated with it, progress will be
	//           between 0 & 100. Otherwise progress will be negative
	UpdateStatus(status Status, progress int)
	// Instructs the client to log informational and debug info, that aren't errors
	LogPrintf(logLevel LogLevel, format string, a ...interface{})
}

// calConn holds all parameters required to implement a calibre connection
type calConn struct {
	clientOpts     ClientOptions
	calibreAddr    string
	calibreInfo    CalibreInitInfo
	deviceInfo     DeviceInfo
	okStr          string
	serverPassword string
	tcpConn        net.Conn
	tcpReader      *bufio.Reader
	ucdb           *UncagedDB
	client         Client
	transferCount  int
	debug          bool
}

// ClientOptions stores all the client specific options that a client needs
// to set to successfully download books
type ClientOptions struct {
	ClientName   string   // The name of the client software
	DeviceName   string   // The name of the device the client software is running on
	DeviceModel  string   // The device model of deviceName
	SupportedExt []string // The ebook extensions our device supports
	CoverDims    struct {
		Width  int
		Height int
	}
}

// CalibreInitInfo is the initial information about itself that Calibre sends when establishing
// a connection
type CalibreInitInfo struct {
	CanSupportLpathChanges bool     `mapstructure:"canSupportLpathChanges"`
	CanSupportUpdateBooks  bool     `mapstructure:"canSupportUpdateBooks"`
	CalibreVersion         []int    `mapstructure:"calibre_version"`
	PubdateFormat          string   `mapstructure:"pubdateFormat"`
	ServerProtocolVersion  int      `mapstructure:"serverProtocolVersion"`
	PasswordChallenge      string   `mapstructure:"passwordChallenge"`
	CurrentLibraryName     string   `mapstructure:"currentLibraryName"`
	TimestampFormat        string   `mapstructure:"timestampFormat"`
	ValidExtensions        []string `mapstructure:"validExtensions"`
	LastModifiedFormat     string   `mapstructure:"lastModifiedFormat"`
	CurrentLibraryUUID     string   `mapstructure:"currentLibraryUUID"`
}

// CalibreInit is used by calibre to determine the software/devices capabilities
type CalibreInit struct {
	WillAskForUpdateBooks         bool           `json:"willAskForUpdateBooks"`
	VersionOK                     bool           `json:"versionOK"`
	MaxBookContentPacketLen       int            `json:"maxBookContentPacketLen"`
	AcceptedExtensions            []string       `json:"acceptedExtensions"`
	ExtensionPathLengths          map[string]int `json:"extensionPathLengths"`
	PasswordHash                  string         `json:"passwordHash"`
	CcVersionNumber               int            `json:"ccVersionNumber"`
	CanStreamBooks                bool           `json:"canStreamBooks"`
	CanStreamMetadata             bool           `json:"canStreamMetadata"`
	CanReceiveBookBinary          bool           `json:"canReceiveBookBinary"`
	CanDeleteMultipleBooks        bool           `json:"canDeleteMultipleBooks"`
	CanUseCachedMetadata          bool           `json:"canUseCachedMetadata"`
	DeviceKind                    string         `json:"deviceKind"`
	UseUUIDFileNames              bool           `json:"useUuidFileNames"`
	CoverHeight                   int            `json:"coverHeight"`
	DeviceName                    string         `json:"deviceName"`
	AppName                       string         `json:"appName"`
	CacheUsesLpaths               bool           `json:"cacheUsesLpaths"`
	CanSendOkToSendbook           bool           `json:"canSendOkToSendbook"`
	CanAcceptLibraryInfo          bool           `json:"canAcceptLibraryInfo"`
	SetTempMarkWhenReadInfoSynced bool           `json:"setTempMarkWhenReadInfoSynced"`
}

// DeviceInfo is used by calibre to determine some more device information, including
// memory location code, uuids, last connect datetime etc.
type DeviceInfo struct {
	DeviceVersion string `json:"device_version" mapstructure:"device_version"`
	Version       string `json:"version" mapstructure:"version"`
	DevInfo       struct {
		Prefix            string    `json:"prefix" mapstructure:"prefix"`
		CalibreVersion    string    `json:"calibre_version" mapstructure:"calibre_version"`
		LastLibraryUUID   string    `json:"last_library_uuid" mapstructure:"last_library_uuid"`
		DeviceName        string    `json:"device_name" mapstructure:"device_name"`
		DateLastConnected time.Time `json:"date_last_connected" mapstructure:"date_last_connected"`
		LocationCode      string    `json:"location_code" mapstructure:"location_code"`
		DeviceStoreUUID   string    `json:"device_store_uuid" mapstructure:"device_store_uuid"`
	} `json:"device_info" mapstructure:"device_info"`
}

// SendBook is used to hold information about each ebook as it arrives
type SendBook struct {
	TotalBooks             int                    `mapstructure:"totalBooks"`
	Lpath                  string                 `mapstructure:"lpath"`
	ThisBook               int                    `mapstructure:"thisBook"`
	WillStreamBinary       bool                   `mapstructure:"willStreamBinary"`
	CanSupportLpathChanges bool                   `mapstructure:"canSupportLpathChanges"`
	Length                 int                    `mapstructure:"length"`
	WillStreamBooks        bool                   `mapstructure:"willStreamBooks"`
	Metadata               map[string]interface{} `mapstructure:"metadata"`
	WantsSendOkToSendbook  bool                   `mapstructure:"wantsSendOkToSendbook"`
}

// DeleteBooks is a list of lpaths to delete
type DeleteBooks struct {
	Lpaths []string `mapstructure:"lpaths"`
}

// BookID identifies one book. Clients may use either field as their
// preferred identification method
type BookID struct {
	Lpath string
	UUID  string
}

// FreeSpace is used to send the available space in bytes to Calibre
type FreeSpace struct {
	FreeSpaceOnDevice uint64 `json:"free_space_on_device"`
}

// MetadataUpdate is used for sending updated metadata to the client
type MetadataUpdate struct {
	Count        int                    `mapstructure:"count"`
	SupportsSync bool                   `mapstructure:"supportsSync"`
	Data         map[string]interface{} `mapstructure:"data"`
	Index        int                    `mapstructure:"index"`
}

// BookCount sends the number of books on device to Calibre
type BookCount struct {
	Count      int  `json:"count"`
	WillStream bool `json:"willStream"`
	WillScan   bool `json:"willScan"`
}

// BookCountDetails sends basic details of each book already
// on device
type BookCountDetails struct {
	PriKey       int       `json:"priKey"`
	UUID         string    `json:"uuid" mapstructure:"uuid"`
	Extension    string    `json:"extension" mapstructure:"extension"`
	Lpath        string    `json:"lpath" mapstructure:"lpath"`
	LastModified time.Time `json:"last_modified" mapstructure:"last_modified"`
}

// GetBook prepares Calibre for the book we are about to send
type GetBook struct {
	WillStream       bool  `json:"willStream"`
	WillStreamBinary bool  `json:"willStreamBinary"`
	FileLength       int64 `json:"fileLength"`
}

// NewLpath informs Calibre of a change in lpath
type NewLpath struct {
	Lpath string `json:"lpath"`
}
