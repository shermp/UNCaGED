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

package uncgd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ricochet2200/go-disk-usage/du"
)

type calOpCode int

// Calibre opcodes
const (
	NOOP                    calOpCode = 12
	OK                      calOpCode = 0
	BOOK_DONE               calOpCode = 11
	CALIBRE_BUSY            calOpCode = 18
	SET_LIBRARY_INFO        calOpCode = 19
	DELETE_BOOK             calOpCode = 13
	DISPLAY_MESSAGE         calOpCode = 17
	FREE_SPACE              calOpCode = 5
	GET_BOOK_FILE_SEGMENT   calOpCode = 14
	GET_BOOK_METADATA       calOpCode = 15
	GET_BOOK_COUNT          calOpCode = 6
	GET_DEVICE_INFORMATION  calOpCode = 3
	GET_INITIALIZATION_INFO calOpCode = 9
	SEND_BOOKLISTS          calOpCode = 7
	SEND_BOOK               calOpCode = 8
	SEND_BOOK_METADATA      calOpCode = 16
	SET_CALIBRE_DEVICE_INFO calOpCode = 1
	SET_CALIBRE_DEVICE_NAME calOpCode = 2
	TOTAL_SPACE             calOpCode = 4
)

// calConn holds all parameters required to implement a calibre connection
type calConn struct {
	clientOpts  ClientOptions
	calibreAddr string
	calibreInfo struct {
		calibreVers    string
		calibreLibUUID string
	}
	okStr         string
	tcpConn       net.Conn
	metadata      []map[string]interface{}
	NewMetadata   []map[string]interface{}
	DelMetadata   []map[string]interface{}
	transferCount int
	tcpReadWait   chan bool
	tcpReader     *bufio.Reader
	Status        chan Status
}

// ClientOptions stores all the client specific options that a client needs
// to set to successfully download books
type ClientOptions struct {
	ClientName  string // The name of the client software
	DeviceName  string // The name of the device the client software is running on
	DeviceModel string // The device model of deviceName
	// Information about a specific store (partition/folder etc.)
	DevStore struct {
		RootDir      string // The root directory of the store (absolute)
		BookDir      string // the book directory to download books to. (relative to RootDir)
		LocationCode string // The location code for the calibre GUI (eg: "main", "cardA")
		UUID         string // The UUID for the device store
	}
	SupportedExt []string // The ebook extensions our device supports
	CoverDims    struct {
		Width  int
		Height int
	}
}

// StatusCode is an opcode to let the calling program know what kind
// of message has been sent on the channel
type StatusCode int

const (
	PrintMsg    StatusCode = iota // Value will contain a string to print
	ProgPercent                   // Value will contain an int between 0 and 100
	ConnectErr
	TCPclosed
)

// Status will be sent on the channel to inform the calling program of the current
// status of CalConn
type Status struct {
	StatCode StatusCode
	Value    interface{}
}

func buildJSONpayload(jsonBytes []byte, op calOpCode) []byte {
	prefix := []byte("[" + strconv.Itoa(int(op)) + ",")
	suffix := []byte("]")
	frameSz := len(prefix) + len(jsonBytes) + len(suffix)
	payloadSz := []byte(strconv.Itoa(frameSz))
	payload := []byte{}
	payload = append(payloadSz, prefix...)
	payload = append(payload, jsonBytes...)
	payload = append(payload, suffix...)
	return payload
}

func delFromSlice(slice []map[string]interface{}, index int) []map[string]interface{} {
	slice[index] = slice[len(slice)-1]
	slice[len(slice)-1] = nil
	slice = slice[:len(slice)-1]
	return slice
}

// New initilizes the calibre connection, and returns it
func New(cliOpts ClientOptions) (calConn, error) {
	var retErr error
	retErr = nil
	var c calConn
	c.clientOpts = cliOpts
	c.Status = make(chan Status, 20)
	c.NewMetadata = make([]map[string]interface{}, 0)
	c.DelMetadata = make([]map[string]interface{}, 0)
	c.metadata = make([]map[string]interface{}, 0)
	c.transferCount = 0
	c.tcpReadWait = make(chan bool)
	c.okStr = "6[0,{}]"
	udpReply := make(chan string)
	bcastPorts := []int{54982, 48123, 39001, 44044, 59678}
	for _, p := range bcastPorts {
		go c.findCalibre(p, udpReply)
	}

	select {
	case addr := <-udpReply:
		c.calibreAddr = addr
	case <-time.After(5 * time.Second):
		retErr = errors.New("calibre server not found")
	}
	return c, retErr
}

// Listen starts a TCP connection with Calibre, then listens
// for messages and pass them to the appropriate handler
func (c *calConn) Listen() {
	c.Status <- Status{StatCode: PrintMsg, Value: "Connecting to Calibre..."}
	// Connect to Calibre
	var err error
	c.tcpConn, err = net.Dial("tcp", c.calibreAddr)
	if err != nil {
		log.Print(err)
		c.Status <- Status{StatCode: PrintMsg, Value: err.Error()}
		return
	}
	c.Status <- Status{StatCode: PrintMsg, Value: "Connected!"}
	c.tcpReader = bufio.NewReader(c.tcpConn)
	// Keep reading untill the connection is closed
	exitListen := false
	for {
		// Read Size of the payload. The payload looks like
		// 13[0,{"foo":1}]
		msgSz, err := c.tcpReader.ReadBytes('[')
		buffLen := len(msgSz)
		// Assume the payload should be less than 10MB!
		if (buffLen > 8) || err != nil {
			if err != nil {
				if err == io.EOF {
					if buffLen <= 0 {
						// Done now
						break
					} else {
						// We may still have a paylad to decode
						exitListen = true
					}
				}
				log.Print(err)
			} else {
				log.Printf("Length too long. Possibly not size.")
			}

		}
		// Put that '[' character back into the buffer. Our JSON
		// parser will need it later...
		c.tcpReader.UnreadByte()
		// We don't want a '[' when we try and convert the byteslice
		// to a number
		if msgSz[buffLen-1] == '[' {
			msgSz = msgSz[:buffLen-1]
		}
		sz, err := strconv.Atoi(string(msgSz))
		if err != nil {
			log.Print(err)
		}
		// We have our payload size. Create the appropriate buffer.
		// and read into it.
		payload := make([]byte, sz)
		io.ReadFull(c.tcpReader, payload)
		// Now that we hopefully have our payload, time to unmarshal it
		var calibreDat []interface{}
		err = json.Unmarshal(payload, &calibreDat)
		// The first element should always be an opcode
		opcode := calOpCode(calibreDat[0].(float64))

		switch opcode {
		case GET_INITIALIZATION_INFO:
			c.getInitInfo(calibreDat)
		case GET_DEVICE_INFORMATION:
			c.getDeviceInfo(calibreDat)
		case SET_CALIBRE_DEVICE_INFO:
			c.tcpConn.Write([]byte(c.okStr))
		case FREE_SPACE:
			c.getFreeSpace()
		case GET_BOOK_COUNT:
			c.getBookCount()
		case SET_LIBRARY_INFO:
			c.tcpConn.Write([]byte(c.okStr))
		case SEND_BOOK:
			c.sendBook(calibreDat)
		case DELETE_BOOK:
			c.deleteBook(calibreDat)
		case NOOP:
			c.handleNoop(calibreDat)
		}
		if exitListen {
			break
		}
	}
	done := Status{StatCode: TCPclosed, Value: nil}
	c.Status <- done
}

func (c *calConn) writeCurrentMetadata() {
	mdPath := filepath.Join(c.clientOpts.DevStore.RootDir, ".metadata.calibre")
	mdFile, err := os.OpenFile(mdPath, os.O_WRONLY|os.O_CREATE, 0666)
	defer mdFile.Close()
	if err != nil {
		log.Print(err)
	}
	mdJSON, _ := json.MarshalIndent(c.metadata, "", "    ")
	mdFile.Write(mdJSON)
	mdFile.Close()
}

func (c *calConn) handleNoop(data []interface{}) {
	calJSON := data[1].(map[string]interface{})
	if len(calJSON) == 0 {
		// Calibre appears to use this opcode as a keep-alive signal
		// We reply to tell callibre is all still good.
		c.tcpConn.Write([]byte(c.okStr))
	}
}

func (c *calConn) getInitInfo(data []interface{}) {
	calSettings := data[1].(map[string]interface{})
	calVersion := calSettings["calibre_version"].([]interface{})
	c.calibreInfo.calibreVers = strconv.Itoa(int(calVersion[0].(float64))) + "." + strconv.Itoa(int(calVersion[1].(float64))) + "." + strconv.Itoa(int(calVersion[2].(float64)))
	c.calibreInfo.calibreLibUUID = calSettings["currentLibraryUUID"].(string)
	//calibreInfo := data[1].(map[string]interface{})
	extPathLen := make(map[string]int)
	for _, e := range c.clientOpts.SupportedExt {
		extPathLen[e] = 38
	}
	initInfo := CalibreInit{
		VersionOK:               true,
		MaxBookContentPacketLen: 4096,
		AcceptedExtensions:      c.clientOpts.SupportedExt,
		ExtensionPathLengths:    extPathLen,
		CcVersionNumber:         391,
		CanStreamBooks:          true,
		CanStreamMetadata:       true,
		CanReceiveBookBinary:    true,
		CanDeleteMultipleBooks:  true,
		CanUseCachedMetadata:    true,
		DeviceKind:              c.clientOpts.DeviceModel,
		DeviceName:              c.clientOpts.DeviceName,
		CoverHeight:             c.clientOpts.CoverDims.Height,
		AppName:                 c.clientOpts.ClientName,
		CacheUsesLpaths:         true,
		CanSendOkToSendbook:     true,
		CanAcceptLibraryInfo:    true,
	}
	initJSON, err := json.Marshal(initInfo)
	if err != nil {
		log.Print(err)
	}
	payload := buildJSONpayload(initJSON, OK)
	c.tcpConn.Write(payload)
}

func (c *calConn) getDeviceInfo(data []interface{}) {
	var devInfo DeviceInfo
	drvInfoPath := filepath.Join(c.clientOpts.DevStore.RootDir, ".driveinfo.calibre")
	drvInfoFile, err := os.OpenFile(drvInfoPath, os.O_RDWR|os.O_CREATE, 0666)
	defer drvInfoFile.Close()
	if err == nil {
		fi, _ := drvInfoFile.Stat()
		if fi.Size() > 0 {
			drvInfoJSON, _ := ioutil.ReadAll(drvInfoFile)
			json.Unmarshal(drvInfoJSON, &devInfo.DevInfo)
		} else {
			devInfo.DevInfo.CalibreVersion = c.calibreInfo.calibreVers
			devInfo.DevInfo.LastLibraryUUID = c.calibreInfo.calibreLibUUID
			devInfo.DevInfo.DateLastConnected = time.Now().Truncate(time.Second)
			devInfo.DevInfo.DeviceName = c.clientOpts.DeviceName
			devInfo.DevInfo.DeviceStoreUUID = c.clientOpts.DevStore.UUID
			devInfo.DevInfo.LocationCode = c.clientOpts.DevStore.LocationCode
			devInfo.DevInfo.Prefix = c.clientOpts.DevStore.BookDir
		}
	} else {
		log.Print(err)
	}
	devInfo.DeviceVersion = c.clientOpts.DeviceModel
	devInfo.Version = "391"
	devInfoJSON, _ := json.Marshal(devInfo)
	payload := buildJSONpayload(devInfoJSON, OK)
	c.tcpConn.Write(payload)
	devInfo.DevInfo.CalibreVersion = c.calibreInfo.calibreVers
	devInfo.DevInfo.LastLibraryUUID = c.calibreInfo.calibreLibUUID
	devInfo.DevInfo.DateLastConnected = time.Now().Truncate(time.Second)
	drvInfoJSON, _ := json.MarshalIndent(devInfo.DevInfo, "", "    ")
	drvInfoFile.Write(drvInfoJSON)
	drvInfoFile.Close()
}

func (c *calConn) getFreeSpace() {
	usage := du.NewDiskUsage(c.clientOpts.DevStore.RootDir)
	var space FreeSpace
	space.FreeSpaceOnDevice = usage.Available()
	fsJSON, _ := json.Marshal(space)
	payload := buildJSONpayload(fsJSON, OK)
	c.tcpConn.Write(payload)
}

func (c *calConn) getBookCount() {
	var bookDetails []BookCountDetails
	count := BookCount{Count: 0, WillStream: true, WillScan: true}
	mdPath := filepath.Join(c.clientOpts.DevStore.RootDir, ".metadata.calibre")
	mdFile, err := os.OpenFile(mdPath, os.O_RDWR|os.O_CREATE, 0666)
	defer mdFile.Close()
	if err == nil {
		fi, _ := mdFile.Stat()
		if fi.Size() > 0 {
			mdJSON, _ := ioutil.ReadAll(mdFile)
			json.Unmarshal(mdJSON, &c.metadata)
		} else {
			mdFile.Write([]byte("[]\n"))
		}
	}
	if c.metadata != nil && len(c.metadata) > 0 {
		for _, md := range c.metadata {
			var bd BookCountDetails
			bd.Lpath = md["lpath"].(string)
			bd.LastModified, _ = time.Parse(time.RFC3339, md["last_modified"].(string))
			bd.UUID = md["uuid"].(string)
			bookDetails = append(bookDetails, bd)
		}
	}
	if bookDetails != nil && len(bookDetails) > 0 {
		count.Count = len(bookDetails)
	}
	bcJSON, _ := json.Marshal(count)
	payload := buildJSONpayload(bcJSON, OK)
	c.tcpConn.Write(payload)
	if count.Count > 0 {
		for _, bd := range bookDetails {
			bdJSON, _ := json.Marshal(bd)
			payload = buildJSONpayload(bdJSON, OK)
			c.tcpConn.Write(payload)
		}
	}
}

func (c *calConn) sendBook(data []interface{}) {
	calJSON := data[1].(map[string]interface{})
	if c.transferCount == 0 {
		c.transferCount = int(calJSON["totalBooks"].(float64))
	}
	userMetadata := calJSON["metadata"].(map[string]interface{})
	lPath := calJSON["lpath"].(string)
	bookPath := filepath.Join(c.clientOpts.DevStore.RootDir, lPath)
	bookLen := int64(calJSON["length"].(float64))
	bookFile, err := os.OpenFile(bookPath, os.O_WRONLY|os.O_CREATE, 0666)
	defer bookFile.Close()
	if err != nil {
		log.Printf("Could not open ebook file!")
		return
	}
	wantsOK := calJSON["wantsSendOkToSendbook"].(bool)
	if wantsOK {
		c.tcpConn.Write([]byte(c.okStr))
	}
	written, err := io.CopyN(bookFile, c.tcpReader, bookLen)
	if err == nil {
		if written != bookLen {
			log.Printf("There was an error recieving the ebook!")
		}
	}
	existingBook := false
	for _, md := range c.metadata {
		if strings.Compare(md["uuid"].(string), userMetadata["uuid"].(string)) == 0 {
			existingBook = true
			md = userMetadata
			break
		}
	}
	for i, md := range c.DelMetadata {
		if strings.Compare(md["uuid"].(string), userMetadata["uuid"].(string)) == 0 {
			// If we're reading a book we previously deleted in this session, remove
			// it from the deleted metadata array
			c.DelMetadata = delFromSlice(c.DelMetadata, i)
			break
		}
	}
	if !existingBook {
		c.NewMetadata = append(c.NewMetadata, userMetadata)
		c.metadata = append(c.metadata, userMetadata)
	}
	c.transferCount--
	bookFile.Close()
	// If we've finished this set of transfers, write out the conanical metadata
	// to file.
	if c.transferCount == 0 {
		c.writeCurrentMetadata()
	}
}

func (c *calConn) deleteBook(data []interface{}) {
	c.tcpConn.Write([]byte(c.okStr))
	delJSON := data[1].(map[string]interface{})
	lpathsInterArr := delJSON["lpaths"].([]interface{})
	lpaths := make([]string, len(lpathsInterArr))
	for i, lp := range lpathsInterArr {
		lpaths[i] = lp.(string)
	}
	for _, lp := range lpaths {
		path := filepath.Join(c.clientOpts.DevStore.RootDir, lp)
		os.Remove(path)
		for i, md := range c.metadata {
			if strings.Compare(md["lpath"].(string), lp) == 0 {
				// Confirm to Calibre that we have deleted the correct book
				uuidMap := map[string]string{"uuid": md["uuid"].(string)}
				uuidJSON, _ := json.Marshal(uuidMap)
				payload := buildJSONpayload(uuidJSON, OK)
				c.tcpConn.Write(payload)
				// Delete the current book from the main metadata
				c.metadata = delFromSlice(c.metadata, i)
				break
			}
		}
		for i, md := range c.NewMetadata {
			if strings.Compare(md["lpath"].(string), lp) == 0 {
				c.NewMetadata = delFromSlice(c.NewMetadata, i)
				break
			}
		}
	}
	c.writeCurrentMetadata()
}

func (c *calConn) findCalibre(bcastPort int, calibreAddr chan<- string) {
	localAddress := "0.0.0.0:0"
	portStr := fmt.Sprintf("%d", bcastPort)
	bcastAddress := "255.255.255.255:" + portStr
	pc, err := net.ListenPacket("udp", localAddress)
	if err != nil {
		log.Print(err)
	}
	defer pc.Close()
	calibreReply := make([]byte, 256)
	udpAddr, _ := net.ResolveUDPAddr("udp", bcastAddress)
	pc.WriteTo([]byte("hello"), udpAddr)
	deadlineTime := time.Now().Add(5 * time.Second)
	pc.SetReadDeadline(deadlineTime)
	bytesRead, addr, err := pc.ReadFrom(calibreReply)
	if err != nil {
		log.Print(err)
		return
	}
	calibreIP, _, _ := net.SplitHostPort(addr.String())
	calibreMsg := string(calibreReply[:bytesRead])
	msgData := strings.Split(calibreMsg, ",")
	calibrePort := msgData[len(msgData)-1]
	select {
	case calibreAddr <- calibreIP + ":" + calibrePort:
		return
	case <-time.After(2 * time.Second):
		return
	}
}
