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
	"encoding/json"
	"io"
	"net"
	"strings"
	"time"

	"github.com/shermp/UNCaGED/calibre"
)

type calOpCode int
type calMsgCode int
type ucdbSearchType int

// LogLevel is the level of logging info that will be sent to the client
type LogLevel int

// Status is a set of pre-defined status codes to be sent to the client
type Status int

// CalError is the type of Calibre specific errors the client should check for
type CalError string

// CalInstance is an alias for calibre.ConnectionInfo. It saves the client
// from having to import another package
type CalInstance = calibre.ConnectionInfo

// Specific Calibre errors that should be handled
const (
	CalibreNotFound CalError = "calibre server not found"
	NoPassword      CalError = "no password found"
)

func (ce CalError) Error() string {
	return string(ce)
}

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
	DeletingBook
	SendingExtraMetadata
	EmptyPasswordReceived
	Waiting
)

// UncagedDB is the structure used by UNCaGED's internal database
type UncagedDB struct {
	nextKey  int
	booklist []BookCountDetails
}

// MetadataIter allows the client to lazy load book metadata
type MetadataIter interface {
	// Next advances the iterator. Returns false when done, true otherwise
	Next() bool
	// Count returns the expected number of iterations. Required because Calibre
	// needs to know how many metadata items will be sent
	Count() int
	// Get the metadata at the current position. Returns an error if the iterator
	// can not continue
	Get() (CalibreBookMeta, error)
}

// Client is the interface that specific implementations of UNCaGED must implement.
// Errors will be returned as-is.
type Client interface {
	// SelectCalibreInstance allows the client to choose a calibre instance if multiple
	// are found on the network
	// The function should return the instance to use
	SelectCalibreInstance(calInstances []CalInstance) CalInstance
	// GetClientOptions returns all the client specific options required for UNCaGED
	GetClientOptions() (opts ClientOptions, err error)
	// GetDeviceBookList returns a slice of all the books currently on the device
	// A nil slice is interpreted has having no books on the device
	GetDeviceBookList() (booklist []BookCountDetails, err error)
	// GetMetadataList sends complete metadata for the books listed in lpaths, or for
	// all books on device if lpaths is empty
	//GetMetadataList(books []BookID) (mdList []CalibreBookMeta, err error)
	// GetMetadataIter should return an iterator that provides metadata for all books in 'books',
	// or all books on device if 'books' is nil
	GetMetadataIter(books []BookID) MetadataIter
	// GetDeviceInfo asks the client for information about the drive info to use
	GetDeviceInfo() (DeviceInfo, error)
	// SetDeviceInfo sets the new device info, as comes from calibre. Only the nested
	// struct DevInfo is modified.
	SetDeviceInfo(devInfo DeviceInfo) error
	// SetLibraryInfo provides the client with some information about the currently connected library
	SetLibraryInfo(libInfo CalibreLibraryInfo) error
	// UpdateMetadata instructs the client to update their metadata according to the
	// new slice of metadata maps
	UpdateMetadata(mdList []CalibreBookMeta) error
	// GetPassword gets a password from the user.
	GetPassword(calibreInfo CalibreInitInfo) (password string, err error)
	// GetFreeSpace reports the amount of free storage space to Calibre
	GetFreeSpace() uint64
	// CheckLpath asks the client to verify a provided Lpath, and change it if required
	// Return the original string if the Lpath does not need changing
	CheckLpath(lpath string) (newLpath string)
	// SaveBook saves a book with the provided metadata to the disk.
	// Implementations saves the book from the provided io.Reader, which will be 'len' bytes long
	// lastBook informs the client that this is the last book for this transfer
	// newLpath informs UNCaGED of an Lpath change. Use this if the lpath field in md is
	// not valid (eg filesystem limitations.). Return an empty string if original lpath is valid
	SaveBook(md CalibreBookMeta, book io.Reader, len int, lastBook bool) error
	// GetBook provides an io.ReadCloser, and the file len, from which UNCaGED can send the requested book to Calibre
	// NOTE: filePos > 0 is not currently implemented in the Calibre source code, but that could
	// change at any time, so best to handle it anyway.
	GetBook(book BookID, filePos int64) (bookIO io.ReadCloser, size int64, err error)
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
	// SetExitChannel provides the client with a channel to prematurely stop UNCaGED.
	// when true is sent on the channel, UNCaGED will stop after finishing the current job.
	// UNCaGED will exit Start() with a nil error if no other errors were detected
	SetExitChannel(exitChan chan<- bool)
}

// calConn holds all parameters required to implement a calibre connection
type calConn struct {
	clientOpts      ClientOptions
	calibreInstance CalInstance
	calibreInfo     CalibreInitInfo
	deviceInfo      DeviceInfo
	okStr           string
	serverPassword  string
	tcpConn         net.Conn
	tcpReader       *bufio.Reader
	tcpDeadline     struct {
		stdDuration time.Duration
		altDuration time.Duration
	}
	ucdb          *UncagedDB
	client        Client
	transferCount int
	debug         bool
}

type calPayload struct {
	op      calOpCode
	payload json.RawMessage
	err     error
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
	DirectConnect CalInstance
}

// CalibreInitInfo is the initial information about itself that Calibre sends when establishing
// a connection
type CalibreInitInfo struct {
	CanSupportLpathChanges bool     `json:"canSupportLpathChanges"`
	CanSupportUpdateBooks  bool     `json:"canSupportUpdateBooks"`
	CalibreVersion         []int    `json:"calibre_version"`
	PubdateFormat          string   `json:"pubdateFormat"`
	ServerProtocolVersion  int      `json:"serverProtocolVersion"`
	PasswordChallenge      string   `json:"passwordChallenge"`
	CurrentLibraryName     string   `json:"currentLibraryName"`
	TimestampFormat        string   `json:"timestampFormat"`
	ValidExtensions        []string `json:"validExtensions"`
	LastModifiedFormat     string   `json:"lastModifiedFormat"`
	CurrentLibraryUUID     string   `json:"currentLibraryUUID"`
}

// CalibreLibraryInfo contains basic library information about the currently connected
// calibre library, such as library name, uuid, and fields
type CalibreLibraryInfo struct {
	FieldMetadata map[string]CalibreColumnInfo `json:"fieldMetadata"`
	LibraryUUID   string                       `json:"libraryUuid"`
	LibraryName   string                       `json:"libraryName"`
	OtherInfo     interface{}                  `json:"otherInfo"`
}

// CalibreColumnInfo is a simplified subset of a CalibreCustomColumn
type CalibreColumnInfo struct {
	ColNum       int                   `json:"colnum"`
	RecIndex     int                   `json:"rec_index"`
	Label        string                `json:"label"`
	Datatype     CalibreColumnDataType `json:"datatype"`
	Name         string                `json:"name"`
	CategorySort string                `json:"category_sort"`
	IsCsp        bool                  `json:"is_csp"`
	Kind         string                `json:"kind"`
	IsCustom     bool                  `json:"is_custom"`
	IsEditable   bool                  `json:"is_editable"`
	Column       string                `json:"column"`
	SearchTerms  []string              `json:"search_terms"`
	IsCategory   bool                  `json:"is_category"`
	Table        string                `json:"table"`
	Display      json.RawMessage       `json:"display"`
	LinkColumn   string                `json:"link_column"`
}

// CalibreColumnDataType is the data type the column holds
type CalibreColumnDataType string

// KnownType checks whether the data type is known to UC
func (t *CalibreColumnDataType) KnownType() bool {
	switch *t {
	case "int",
		"series",
		"bool",
		"text",
		"composite",
		"rating",
		"comments",
		"enumeration",
		"datetime",
		"float":
		return true
	}
	return false
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
	DeviceVersion string `json:"device_version"`
	Version       string `json:"version"`
	DevInfo       struct {
		Prefix            string    `json:"prefix"`
		CalibreVersion    string    `json:"calibre_version"`
		LastLibraryUUID   string    `json:"last_library_uuid"`
		DeviceName        string    `json:"device_name"`
		DateLastConnected time.Time `json:"date_last_connected"`
		LocationCode      string    `json:"location_code"`
		DeviceStoreUUID   string    `json:"device_store_uuid"`
	} `json:"device_info"`
}

// SendBook is used to hold information about each ebook as it arrives
type SendBook struct {
	TotalBooks             int             `json:"totalBooks"`
	Lpath                  string          `json:"lpath"`
	ThisBook               int             `json:"thisBook"`
	WillStreamBinary       bool            `json:"willStreamBinary"`
	CanSupportLpathChanges bool            `json:"canSupportLpathChanges"`
	Length                 int             `json:"length"`
	WillStreamBooks        bool            `json:"willStreamBooks"`
	Metadata               CalibreBookMeta `json:"metadata"`
	WantsSendOkToSendbook  bool            `json:"wantsSendOkToSendbook"`
}

// DeleteBooks is a list of lpaths to delete
type DeleteBooks struct {
	Lpaths []string `json:"lpaths"`
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
	Count        int             `json:"count"`
	SupportsSync bool            `json:"supportsSync"`
	Data         CalibreBookMeta `json:"data"`
	Index        int             `json:"index"`
}

// BookCountSend sends the number of books on device to Calibre
type BookCountSend struct {
	Count      int  `json:"count"`
	WillStream bool `json:"willStream"`
	WillScan   bool `json:"willScan"`
}

// BookCountReceive contains the bookcount options calibre sends
type BookCountReceive struct {
	CanStream                bool `json:"canStream"`
	CanScan                  bool `json:"canScan"`
	WillUseCachedMetadata    bool `json:"willUseCachedMetadata"`
	SupportsSync             bool `json:"supportsSync"`
	CanSupportBookFormatSync bool `json:"canSupportBookFormatSync"`
}

// BookCountDetails sends basic details of each book already
// on device
type BookCountDetails struct {
	PriKey       int       `json:"priKey"`
	UUID         string    `json:"uuid"`
	Extension    string    `json:"extension"`
	Lpath        string    `json:"lpath"`
	LastModified time.Time `json:"last_modified"`
}

// GetBookSend prepares Calibre for the book we are about to send
type GetBookSend struct {
	WillStream       bool  `json:"willStream"`
	WillStreamBinary bool  `json:"willStreamBinary"`
	FileLength       int64 `json:"fileLength"`
}

// GetBookReceive contains the settings calibre sends when requesting a book
type GetBookReceive struct {
	Lpath           string `json:"lpath"`
	Position        int64  `json:"position"`
	ThisBook        int    `json:"thisBook"`
	TotalBooks      int    `json:"totalBooks"`
	CanStream       bool   `json:"canStream"`
	CanStreamBinary bool   `json:"canStreamBinary"`
}

// NewLpath informs Calibre of a change in lpath
type NewLpath struct {
	Lpath string `json:"lpath"`
}

// BookListsDetails is sent from calibre to prepare for receiving metadata
type BookListsDetails struct {
	Count              int         `json:"count"`
	Collections        interface{} `json:"collections"`
	WillStreamMetadata bool        `json:"willStreamMetadata"`
	SupportsSync       bool        `json:"supportsSync"`
}

// CalibreBookMeta contains top level metadata fields for a book from Calibre
type CalibreBookMeta struct {
	Authors         []string                       `json:"authors"`
	Languages       []string                       `json:"languages"`
	UserMetadata    map[string]CalibreCustomColumn `json:"user_metadata"`
	UserCategories  map[string]interface{}         `json:"user_categories"`
	Comments        *string                        `json:"comments"`
	Tags            []string                       `json:"tags"`
	Pubdate         *CalibreTime                   `json:"pubdate"`
	SeriesIndex     *float64                       `json:"series_index"`
	Thumbnail       CalibreThumb                   `json:"thumbnail"`
	PublicationType *string                        `json:"publication_type"`
	Mime            *string                        `json:"mime"`
	AuthorSort      string                         `json:"author_sort"`
	Series          *string                        `json:"series"`
	Rights          *string                        `json:"rights"`
	DbID            interface{}                    `json:"db_id"`
	Cover           *string                        `json:"cover"`
	ApplicationID   interface{}                    `json:"application_id"`
	BookProducer    *string                        `json:"book_producer"`
	Size            int                            `json:"size"`
	AuthorSortMap   map[string]string              `json:"author_sort_map"`
	Rating          *float64                       `json:"rating"`
	Lpath           string                         `json:"lpath"`
	Publisher       *string                        `json:"publisher"`
	Timestamp       *CalibreTime                   `json:"timestamp"`
	LastModified    *CalibreTime                   `json:"last_modified"`
	UUID            string                         `json:"uuid"`
	TitleSort       string                         `json:"title_sort"`
	AuthorLinkMap   map[string]string              `json:"author_link_map"`
	Title           string                         `json:"title"`
	Identifiers     map[string]string              `json:"identifiers"`
}

// LangString returns the string representation of the 'language' field
func (m *CalibreBookMeta) LangString() string {
	return strings.Join(m.Languages, ",")
}

// TagString returns the list of tags (if any) as a comma separated string
func (m *CalibreBookMeta) TagString() string {
	return strings.Join(m.Tags, ",")
}

// PubString returns the publisher as a string, or the empty string no no publisher is set
func (m *CalibreBookMeta) PubString() string {
	if m.Publisher != nil {
		return *m.Publisher
	}
	return ""
}

// RatingString returns the rating column as a string, in the form of stars
func (m *CalibreBookMeta) RatingString() string {
	if m.Rating != nil {
		return formatRating(int(*m.Rating), false)
	}
	return ""
}

// InitMaps initializes any maps that may be nil
func (m *CalibreBookMeta) InitMaps() {
	if m.UserMetadata == nil {
		m.UserMetadata = make(map[string]CalibreCustomColumn)
	}
	if m.UserCategories == nil {
		m.UserCategories = make(map[string]interface{})
	}
	if m.AuthorSortMap == nil {
		m.AuthorSortMap = make(map[string]string)
	}
	if m.AuthorLinkMap == nil {
		m.AuthorLinkMap = make(map[string]string)
	}
	if m.Identifiers == nil {
		m.Identifiers = make(map[string]string)
	}
}

// CalibreTime holds timestamps from calibre
type CalibreTime string

// GetTime returns a time if there is a valid time, nil otherwise
func (ct *CalibreTime) GetTime() *time.Time {
	if ct != nil {
		if parsedTime, err := time.Parse(time.RFC3339, string(*ct)); err == nil {
			return &parsedTime
		}
	}
	return nil
}

// ConvertTime returns a RFC3339 formatted CalibreTime
func ConvertTime(t time.Time) CalibreTime {
	return CalibreTime(t.Format(time.RFC3339))
}

// ParseTime returns an RFC3339 formatted timestamp as a *CalibreTimem or nil otherwise
func ParseTime(timestamp string) *CalibreTime {
	if _, err := time.Parse(time.RFC3339, timestamp); err == nil {
		ct := CalibreTime(timestamp)
		return &ct
	}
	return nil
}

// CalibreThumb stores a thumbnail from Calibre, with some convenience methods
// to access dimensions, and the Base64 string
type CalibreThumb []interface{}

// Exists checks that the thumbnail variable is a valid thumbnail
func (t CalibreThumb) Exists() bool {
	if t != nil && len(t) == 3 {
		return true
	}
	return false
}

// Dimensions return the width and height of the thumbnail.
// Dimensions are only valid if Exists() is true
func (t CalibreThumb) Dimensions() (width, height int) {
	if t != nil && len(t) == 3 {
		return int(t[0].(float64)), int(t[1].(float64))
	}
	return -1, -1
}

// ImgBase64 returns the base64 encoded string of the image binary
// the base64 string is only valid if Exists() is true
func (t CalibreThumb) ImgBase64() string {
	if t != nil && len(t) == 3 {
		return t[2].(string)
	}
	return ""
}

// Set a CalibreThumb dimensions and base64 string
func (t CalibreThumb) Set(width, height int, imgBase64 string) {
	if t == nil || len(t) != 3 {
		t = make([]interface{}, 3)
	}
	t[0], t[1], t[2] = float64(width), float64(height), imgBase64
}
