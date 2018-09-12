package calconn

import "time"

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

// FreeSpace is used to send the available space in bytes to Calibre
type FreeSpace struct {
	FreeSpaceOnDevice uint64 `json:"free_space_on_device"`
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
	UUID string `json:"uuid"`
	//Extension    string    `json:"extension"`
	Lpath        string    `json:"lpath"`
	LastModified time.Time `json:"last_modified"`
}
