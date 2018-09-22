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

	"github.com/pkg/errors"
	"github.com/ricochet2200/go-disk-usage/du"
)

const tcpDeadlineTimeout = 15

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

// ScreenPrinter is an interface which UNCaGED uses to print messages to
// the users display
type ScreenPrinter interface {
	Println(a ...interface{}) (n int, err error)
}

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
	tcpReader     *bufio.Reader
	scrnPrint     ScreenPrinter
}

// ClientOptions stores all the client specific options that a client needs
// to set to successfully download books
type ClientOptions struct {
	ClientName  string // The name of the client software
	DeviceName  string // The name of the device the client software is running on
	DeviceModel string // The device model of deviceName
	// Information about a specific store (partition/folder etc.)
	DevStore struct {
		//RootDir      string // The root directory of the store (absolute)
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

// buildJSONpayload builds a payload in the format that Calibre expects
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

// Convenience function to remove element(s) from a JSON array
func delFromSlice(slice []map[string]interface{}, index int) []map[string]interface{} {
	slice[index] = slice[len(slice)-1]
	slice[len(slice)-1] = nil
	slice = slice[:len(slice)-1]
	return slice
}

// New initilizes the calibre connection, and returns it
// An error is returned if a Calibre instance cannot be found
func New(cliOpts ClientOptions, scrnPrnt ScreenPrinter) (*calConn, error) {
	var retErr error
	retErr = nil
	c := &calConn{}
	c.clientOpts = cliOpts
	os.MkdirAll(c.clientOpts.DevStore.BookDir, 0644)
	c.NewMetadata = make([]map[string]interface{}, 0)
	c.DelMetadata = make([]map[string]interface{}, 0)
	c.metadata = make([]map[string]interface{}, 0)
	c.transferCount = 0
	c.okStr = "6[0,{}]"
	c.scrnPrint = scrnPrnt
	udpReply := make(chan string)
	// Calibre listens for a 'hello' UDP packet on the following
	// five ports. We try all five ports concurrently
	bcastPorts := []int{54982, 48123, 39001, 44044, 59678}
	for _, p := range bcastPorts {
		go c.findCalibre(p, udpReply)
	}

	select {
	// We choose the first reply we recieve, which is a string
	// containing the IP address and port to connect to
	case addr := <-udpReply:
		c.calibreAddr = addr
	// A timeout just in case we receive no reply
	case <-time.After(5 * time.Second):
		retErr = errors.New("calibre server not found")
	}
	return c, retErr
}

// Listen starts a TCP connection with Calibre, then listens
// for messages and pass them to the appropriate handler
func (c *calConn) Listen() (err error) {
	c.scrnPrint.Println("Connecting to Calibre...")
	// Connect to Calibre
	c.tcpConn, err = net.Dial("tcp", c.calibreAddr)
	if err != nil {
		return errors.Wrap(err, "calibre connection failed")
	}
	defer c.tcpConn.Close()
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
	c.scrnPrint.Println("Connected to Calibre!")
	c.tcpReader = bufio.NewReader(c.tcpConn)
	// Keep reading untill the connection is closed
	exitListen := false
	for {
		// Read Size of the payload. The payload looks like
		// 13[0,{"foo":1}]
		msgSz, err := c.tcpReader.ReadBytes('[')
		buffLen := len(msgSz)
		if e, ok := err.(net.Error); ok && e.Timeout() {
			return errors.Wrap(err, "connection timed out!")
		}
		// Assume the payload should be less than 10MB!
		if (buffLen > 8) || err != nil {
			if err != nil {
				if err == io.EOF {
					if buffLen <= 0 {
						// Done now
						return nil
					} else {
						// We may still have a paylad to decode
						exitListen = true
					}
				}
				return errors.Wrap(err, "error reading payload preamble")
			} else {
				// Let's try again...
				log.Println("Length too long. Possibly not size.")
				c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
				continue
			}
		}
		c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
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
			return errors.Wrap(err, "error decoding payload size")
		}
		// We have our payload size. Create the appropriate buffer.
		// and read into it.
		payload := make([]byte, sz)
		io.ReadFull(c.tcpReader, payload)
		if e, ok := err.(net.Error); ok && e.Timeout() {
			return errors.Wrap(err, "connection timed out!")
		} else if err != nil {
			return errors.Wrap(err, "did not receive full payload")
		}
		c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
		// Now that we hopefully have our payload, time to unmarshal it
		var calibreDat []interface{}
		err = json.Unmarshal(payload, &calibreDat)
		if err != nil {
			return errors.Wrap(err, "could not unmarshal payload")
		}
		// The first element should always be an opcode
		opcode := calOpCode(calibreDat[0].(float64))

		switch opcode {
		case GET_INITIALIZATION_INFO:
			err = c.getInitInfo(calibreDat)
		case GET_DEVICE_INFORMATION:
			err = c.getDeviceInfo(calibreDat)
		case SET_CALIBRE_DEVICE_INFO:
			err = c.setDeviceInfo(calibreDat)
		case FREE_SPACE:
			err = c.getFreeSpace()
		case GET_BOOK_COUNT:
			err = c.getBookCount()
		case SET_LIBRARY_INFO:
			err = c.writeTCP([]byte(c.okStr))
		case SEND_BOOK:
			err = c.sendBook(calibreDat)
		case DELETE_BOOK:
			err = c.deleteBook(calibreDat)
		case NOOP:
			err = c.handleNoop(calibreDat)
		}
		if err != nil {
			return err
		}
		if exitListen {
			break
		}
	}
	return nil
}

// Write the current metadata to a file on disk
func (c *calConn) writeCurrentMetadata() error {
	mdPath := filepath.Join(c.clientOpts.DevStore.BookDir, ".metadata.calibre")
	mdJSON, _ := json.MarshalIndent(c.metadata, "", "    ")
	err := ioutil.WriteFile(mdPath, mdJSON, 0644)
	if err != nil {
		return errors.Wrap(err, "failed writing metadata")
	}
	return nil
}

// Convenience function to handle writing to our TCP connection, and manage the deadline
func (c *calConn) writeTCP(payload []byte) error {
	_, err := c.tcpConn.Write(payload)
	if e, ok := err.(net.Error); ok && e.Timeout() {
		return errors.Wrap(err, "connection timed out!")
	} else if err != nil {
		return errors.Wrap(err, "write to tcp connection failed")
	}
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
	return nil
}

func (c *calConn) handleNoop(data []interface{}) error {
	calJSON := data[1].(map[string]interface{})
	if len(calJSON) == 0 {
		// Calibre appears to use this opcode as a keep-alive signal
		// We reply to tell callibre is all still good.
		err := c.writeTCP([]byte(c.okStr))
		if err != nil {
			return err
		}
	}
	return nil
}

// getInitInfo handles the request from Calibre to send initialization info.
func (c *calConn) getInitInfo(data []interface{}) error {
	calSettings := data[1].(map[string]interface{})
	// NOTE TO SELF: this is probably no longer necessary, but could be useful again
	// in the future
	calVersion := calSettings["calibre_version"].([]interface{})
	c.calibreInfo.calibreVers = strconv.Itoa(int(calVersion[0].(float64))) + "." +
		strconv.Itoa(int(calVersion[1].(float64))) + "." +
		strconv.Itoa(int(calVersion[2].(float64)))
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
	initJSON, _ := json.Marshal(initInfo)
	payload := buildJSONpayload(initJSON, OK)
	return c.writeTCP(payload)
}

// getDeviceInfo handles the request from Calibre for the device (that's us!)
// to send information about itself
func (c *calConn) getDeviceInfo(data []interface{}) error {
	var devInfo DeviceInfo
	// Information about the previous connection (if any) is stored in a
	// '.driveinfo.calibre' file in the book directory
	drvInfoPath := filepath.Join(c.clientOpts.DevStore.BookDir, ".driveinfo.calibre")
	drvInfoJSON, err := ioutil.ReadFile(drvInfoPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return errors.Wrap(err, "could not open driveinfo file")
		}
	}
	if drvInfoJSON != nil && len(drvInfoJSON) > 0 {
		json.Unmarshal(drvInfoJSON, &devInfo.DevInfo)
	} else {
		// If the file doesn't exist, or is empty, we create some default values
		// Calibre will send us updated driveinfo in the next packet
		devInfo.DevInfo.CalibreVersion = ""
		devInfo.DevInfo.LastLibraryUUID = ""
		devInfo.DevInfo.DateLastConnected = time.Now().Truncate(time.Second)
		devInfo.DevInfo.DeviceName = c.clientOpts.DeviceName
		devInfo.DevInfo.DeviceStoreUUID = c.clientOpts.DevStore.UUID
		devInfo.DevInfo.LocationCode = c.clientOpts.DevStore.LocationCode
		devInfo.DevInfo.Prefix = ""
	}

	devInfo.DeviceVersion = c.clientOpts.DeviceModel
	devInfo.Version = "391"
	devInfoJSON, _ := json.Marshal(devInfo)
	payload := buildJSONpayload(devInfoJSON, OK)
	return c.writeTCP(payload)
}

// setDeviceInfo saves the return information we got from Calibre
// to place in the '.driveinfo.calibre' file
func (c *calConn) setDeviceInfo(data []interface{}) error {
	calDevInfo := data[1].(map[string]interface{})
	drvJSON, err := json.MarshalIndent(calDevInfo, "", "    ")
	if err != nil {
		return errors.Wrap(err, "problem saving calibre device info")
	}
	drvPath := filepath.Join(c.clientOpts.DevStore.BookDir, ".driveinfo.calibre")
	err = ioutil.WriteFile(drvPath, drvJSON, 0644)
	if err != nil {
		return errors.Wrap(err, "could not write to .driveinfo.calibre")
	}
	return c.writeTCP([]byte(c.okStr))
}

// getFreeSpace tells Calibre how much space is available in our
// book directory.
func (c *calConn) getFreeSpace() error {
	usage := du.NewDiskUsage(c.clientOpts.DevStore.BookDir)
	var space FreeSpace
	space.FreeSpaceOnDevice = usage.Available()
	fsJSON, _ := json.Marshal(space)
	payload := buildJSONpayload(fsJSON, OK)
	return c.writeTCP(payload)
}

// getBookCount sends Calibre a list of ebooks currently on the device,
// as known about in the '.metadata.calibre' file. It does NOT confirm the
// continuing existance of the book on the device.
func (c *calConn) getBookCount() error {
	var bookDetails []BookCountDetails
	// Set the initial count of books to zero. This is what we send
	// if we have no books in our metadata file.
	count := BookCount{Count: 0, WillStream: true, WillScan: true}
	mdPath := filepath.Join(c.clientOpts.DevStore.BookDir, ".metadata.calibre")
	mdFile, err := os.OpenFile(mdPath, os.O_RDWR|os.O_CREATE, 0666)
	defer mdFile.Close()
	if err == nil {
		fi, _ := mdFile.Stat()
		if fi.Size() > 0 {
			mdJSON, _ := ioutil.ReadAll(mdFile)
			json.Unmarshal(mdJSON, &c.metadata)
		} else {
			// If the file is new, initialize it with an empty JSON struct
			mdFile.Write([]byte("[]\n"))
		}
	}
	// If we have metadata, collect it now
	if c.metadata != nil && len(c.metadata) > 0 {
		for _, md := range c.metadata {
			var bd BookCountDetails
			bd.Lpath = md["lpath"].(string)
			bd.LastModified, _ = time.Parse(time.RFC3339, md["last_modified"].(string))
			bd.UUID = md["uuid"].(string)
			bookDetails = append(bookDetails, bd)
		}
	}
	// And update our count
	if bookDetails != nil && len(bookDetails) > 0 {
		count.Count = len(bookDetails)
	}
	bcJSON, _ := json.Marshal(count)
	payload := buildJSONpayload(bcJSON, OK)
	// Send our count
	err = c.writeTCP(payload)
	if err != nil {
		return err
	}
	// If we have books on device, send the metadata for each book
	if count.Count > 0 {
		for _, bd := range bookDetails {
			bdJSON, _ := json.Marshal(bd)
			payload = buildJSONpayload(bdJSON, OK)
			err = c.writeTCP(payload)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// sendBook is where the magic starts to happen. It recieves one
// or more books from calibre.
func (c *calConn) sendBook(data []interface{}) error {
	calJSON := data[1].(map[string]interface{})
	// c.transferCount lets us keep track of the number of books
	// we expect Calibre to send.
	if c.transferCount == 0 {
		c.transferCount = int(calJSON["totalBooks"].(float64))
	}
	userMetadata := calJSON["metadata"].(map[string]interface{})
	lPath := calJSON["lpath"].(string)
	bookPath := filepath.Join(c.clientOpts.DevStore.BookDir, lPath)
	basePath, _ := filepath.Split(bookPath)
	// Calibre can specify subdirectories. We need to make sure these exist
	// before attempting to create/open the ebook file.
	err := os.MkdirAll(basePath, 0644)
	if err != nil {
		return errors.Wrap(err, "could not create ebook directory")
	}
	bookLen := int64(calJSON["length"].(float64))
	bookFile, err := os.OpenFile(bookPath, os.O_WRONLY|os.O_CREATE, 0644)
	defer bookFile.Close()
	if err != nil {
		return errors.Wrap(err, "could not open ebook file")
	}
	wantsOK := calJSON["wantsSendOkToSendbook"].(bool)
	if wantsOK {
		err = c.writeTCP([]byte(c.okStr))
		if err != nil {
			return err
		}
	}
	written, err := io.CopyN(bookFile, c.tcpReader, bookLen)
	if err == nil {
		if written != bookLen {
			bookFile.Close()
			os.Remove(bookPath)
			return errors.New("ebook did not transfer correctly")
		}
	} else {
		bookFile.Close()
		os.Remove(bookPath)
		return errors.Wrap(err, "error saving ebook file")
	}
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))

	// If the book already exists, we update the metadata
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
			// If we're sending a book we previously deleted in this session, remove
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
		err = c.writeCurrentMetadata()
		if err != nil {
			return err
		}
	}
	return nil
}

// deleteBook will delete any ebook Calibre tells us to
func (c *calConn) deleteBook(data []interface{}) error {
	err := c.writeTCP([]byte(c.okStr))
	if err != nil {
		return err
	}
	delJSON := data[1].(map[string]interface{})
	lpathsInterArr := delJSON["lpaths"].([]interface{})
	lpaths := make([]string, len(lpathsInterArr))
	for i, lp := range lpathsInterArr {
		lpaths[i] = lp.(string)
	}
	for _, lp := range lpaths {
		path := filepath.Join(c.clientOpts.DevStore.BookDir, lp)
		os.Remove(path)
		for i, md := range c.metadata {
			if strings.Compare(md["lpath"].(string), lp) == 0 {
				// Confirm to Calibre that we have deleted the correct book
				uuidMap := map[string]string{"uuid": md["uuid"].(string)}
				uuidJSON, _ := json.Marshal(uuidMap)
				payload := buildJSONpayload(uuidJSON, OK)
				c.writeTCP(payload)
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
	return c.writeCurrentMetadata()
}

// findCalibre performs the original search for a Calibre instance, using
// UDP. Note, if there are multple calibre instances with their wireless
// connection active, we select the first that responds.
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
	if e, ok := err.(net.Error); ok && e.Timeout() {
		pc.Close()
		return
	} else if err != nil {
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
