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
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/shermp/UNCaGED/calibre"
)

const bookPacketContentLen = 4096

// buildJSONpayload builds a payload in the format that Calibre expects
func buildJSONpayload(data interface{}, op calOpCode) []byte {
	jsonBytes, _ := json.Marshal(data)
	// Take the Calibre approach of building the payload
	frame := fmt.Sprintf("[%d,%s]", op, jsonBytes)
	payload := []byte(fmt.Sprintf("%d%s", len(frame), frame))
	return payload
}

// New initilizes the calibre connection, and returns it
// An error is returned if a Calibre instance cannot be found
func New(client Client, enableDebug bool) (*calConn, error) {
	var retErr error
	retErr = nil
	c := &calConn{}
	c.debug = enableDebug
	c.client = client
	c.clientOpts, retErr = c.client.GetClientOptions()
	if retErr != nil {
		return nil, fmt.Errorf("New: Error getting client options: %w", retErr)
	}
	c.transferCount = 0
	c.okStr = "6[0,{}]"
	c.tcpDeadline.stdDuration = 60 * time.Second
	c.ucdb = &UncagedDB{}
	bookList, retErr := c.client.GetDeviceBookList()
	if retErr != nil {
		return nil, fmt.Errorf("New: Error getting booklist from device: %w", retErr)
	}
	c.ucdb.initDB(bookList)
	if c.deviceInfo, retErr = c.client.GetDeviceInfo(); retErr != nil {
		return nil, fmt.Errorf("New: Error getting info from device: %w", retErr)
	}
	if c.clientOpts.DirectConnect.Host != "" && c.clientOpts.DirectConnect.TCPPort > 0 {
		ip := net.ParseIP(c.clientOpts.DirectConnect.Host)
		if ip == nil {
			hosts, err := net.LookupHost(c.clientOpts.DirectConnect.Host)
			if err != nil {
				return nil, fmt.Errorf("New: unable to resolve direct connection host: %w", err)
			}
			c.clientOpts.DirectConnect.Host = hosts[0]
		}
		c.calibreInstance = c.clientOpts.DirectConnect
	} else {
		// Calibre listens for a 'hello' UDP packet on the following
		// five ports. We try all five ports concurrently
		c.client.UpdateStatus(SearchingCalibre, -1)
		instances, err := calibre.DiscoverSmartDevice(c)
		if err != nil {
			return nil, fmt.Errorf("New: error getting calibre instances: %w", err)
		}
		if len(instances) == 0 {
			return nil, fmt.Errorf("New: Could not find calibre instance: %w", CalibreNotFound)
		}
		c.calibreInstance = c.client.SelectCalibreInstance(instances)
	}
	return c, retErr
}

// newPriKey returns a new, unique primary key
func (ucdb *UncagedDB) newPriKey() int {
	key := ucdb.nextKey
	ucdb.nextKey++
	return key
}

// findByPriKey searches the 'db' for a record via a key. If no record found,
// error will not be nil.
func (ucdb *UncagedDB) find(searchType ucdbSearchType, value interface{}) (int, BookCountDetails, error) {
	bd := BookCountDetails{}
	var index int
	var err error
	err = fmt.Errorf("find: no match")
	// Simple linear search. Not very efficient, be we shouldn't be doing this too often
	switch searchType {
	case PriKey:
		if k, ok := value.(int); ok {
			for i, b := range ucdb.booklist {
				if b.PriKey == k {
					index = i
					bd = b
					err = nil
					break
				}
			}
		} else {
			err = fmt.Errorf("find: invalid type. Expecting integer")
		}
	case Lpath:
		if l, ok := value.(string); ok {
			for i, b := range ucdb.booklist {
				if b.Lpath == l {
					index = i
					bd = b
					err = nil
					break
				}
			}
		} else {
			err = fmt.Errorf("find: invalid type. Expecting string")
		}
	}
	return index, bd, err
}

func (ucdb *UncagedDB) length() int {
	return len(ucdb.booklist)
}

// addEntry adds a book to our internal "DB"
func (ucdb *UncagedDB) addEntry(md CalibreBookMeta) {
	bd := BookCountDetails{
		PriKey: ucdb.newPriKey(),
		UUID:   md.UUID,
		Lpath:  md.Lpath,
	}
	ucdb.booklist = append(ucdb.booklist, bd)
}

// removeEntry removes a book from our internal "DB"
func (ucdb *UncagedDB) removeEntry(searchType ucdbSearchType, value interface{}) error {
	index, _, err := ucdb.find(searchType, value)
	if err != nil {
		return fmt.Errorf("removeEntry: search failed: %w", err)
	}
	ucdb.booklist = append(ucdb.booklist[:index], ucdb.booklist[index+1:]...)
	return nil
}

// initDB initialises the database with a new booklist
func (ucdb *UncagedDB) initDB(bl []BookCountDetails) {
	ucdb.booklist = bl
	for i := range ucdb.booklist {
		ucdb.booklist[i].PriKey = ucdb.newPriKey()
	}
}

// Start starts a TCP connection with Calibre, then listens
// for messages and pass them to the appropriate handler
func (c *calConn) Start() (err error) {
	exitChan := make(chan bool)
	calPl := make(chan calPayload)
	c.client.SetExitChannel(exitChan)
	c.client.UpdateStatus(Connecting, -1)
	err = c.establishTCP()
	if err != nil {
		return fmt.Errorf("Start: establishing connection failed: %w", err)
	}
	defer c.tcpConn.Close()
	// Connect to Calibre
	// Keep reading untill the connection is closed
	for {
		go c.readDecodeCalibrePayloadChan(calPl)
		select {
		case <-exitChan:
			return nil
		case pl := <-calPl:
			if pl.err != nil {
				if pl.err == io.EOF {
					c.LogPrintf("TCP Connection Closed")
					return nil
				}
				return fmt.Errorf("Start: packet reading failed: %w", pl.err)
			}
			c.LogPrintf("Calibre Opcode received: %v\n", pl.op)
			switch pl.op {
			case getInitializationInfo:
				c.LogPrintf("Processing GET_INIT_INFO packet: %.40s\n", string(pl.payload))
				err = c.getInitInfo(pl.payload)
			case displayMessage:
				c.LogPrintf("Processing DISPLAY_NESSAGE packet: %.40s\n", string(pl.payload))
				err = c.handleMessage(pl.payload)
			case getDeviceInformation:
				c.LogPrintf("Processing GET_DEV_INFO packet: %.40s\n", string(pl.payload))
				err = c.getDeviceInfo()
			case setCalibreDeviceInfo:
				c.LogPrintf("Processing SET_CAL_DEV_INFO packet: %.40s\n", string(pl.payload))
				err = c.setDeviceInfo(pl.payload)
			case freeSpace:
				c.LogPrintf("Processing FREE_SPACE packet: %.40s\n", string(pl.payload))
				err = c.getFreeSpace()
			case getBookCount:
				c.LogPrintf("Processing GET_BOOK_COUNT packet: %.40s\n", string(pl.payload))
				err = c.getBookCount(pl.payload)
			case sendBooklists:
				c.LogPrintf("Processing SEND_BOOKLISTS packet: %.40s\n", string(pl.payload))
				err = c.updateDeviceMetadata(pl.payload)
			case setLibraryInfo:
				c.LogPrintf("Processing SET_LIBRARY_INFO packet: %.40s\n", string(pl.payload))
				err = c.setLibraryInfo(pl.payload)
			case sendBook:
				c.LogPrintf("Processing SEND_BOOK packet: %.40s\n", string(pl.payload))
				err = c.sendBook(pl.payload)
			case deleteBook:
				c.LogPrintf("Processing DELETE_BOOK packet: %.40s\n", string(pl.payload))
				err = c.deleteBook(pl.payload)
			case getBookFileSegment:
				c.LogPrintf("Processing GET_BOOK_FILE_SEGMENT packet: %.40s\n", string(pl.payload))
				err = c.getBook(pl.payload)
			case noop:
				c.LogPrintf("Processing NOOP packet: %.40s\n", string(pl.payload))
				err = c.handleNoop(pl.payload)
			}
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return fmt.Errorf("Start: exiting with error: %w", err)
			}
		}
	}
}

func (c *calConn) LogPrintf(format string, a ...interface{}) {
	if c.debug {
		c.client.LogPrintf(Debug, "[DEBUG] "+format, a...)
	}
}

func (c *calConn) decodeCalibrePayload(payload []byte) (calOpCode, json.RawMessage, error) {
	var calibreDat []json.RawMessage
	if err := json.Unmarshal(payload, &calibreDat); err != nil {
		return -1, nil, fmt.Errorf("decodeCalibrePayload: could not unmarshal payload: %w", err)
	}
	// The first element should always be an opcode
	opcode, err := strconv.Atoi(string(calibreDat[0]))
	if err != nil {
		return -1, nil, fmt.Errorf("decodeCalibrePayload: could not decode opcode: %w", err)
	}
	return calOpCode(opcode), calibreDat[1], nil
}

func (c *calConn) readDecodeCalibrePayload() (calOpCode, json.RawMessage, error) {
	payload, err := c.readTCP()
	if err != nil {
		if err == io.EOF {
			c.client.UpdateStatus(Disconnected, -1)
			return noop, nil, err
		}
		return noop, nil, fmt.Errorf("readDecodeCalibrePayload: connection closed: %w", err)
	}
	opcode, data, err := c.decodeCalibrePayload(payload)
	if err != nil {
		return noop, nil, fmt.Errorf("readDecodeCalibrePayload: packet decoding failed: %w", err)
	}
	return opcode, data, nil
}
func (c *calConn) readDecodeCalibrePayloadChan(calPl chan<- calPayload) {
	pl := calPayload{}
	pl.op, pl.payload, pl.err = c.readDecodeCalibrePayload()
	calPl <- pl
}

// hashCalPassword generates a string representation in hex of the password
// hash Calibre expects. Yes, I know this is not the way password handling should
// be done. Go take it up with the Calibre devs if you want better security...
func (c *calConn) hashCalPassword(challenge string) string {
	shaHash := ""
	passToHash := c.serverPassword + challenge
	h := sha1.New()
	h.Write([]byte(passToHash))
	shaHash = hex.EncodeToString(h.Sum(nil))
	return shaHash
}

func (c *calConn) setTCPDeadline() {
	if c.tcpDeadline.altDuration > 0 {
		c.LogPrintf("setTCPDeadline: setting TCP deadline to %d milliseconds", c.tcpDeadline.altDuration.Milliseconds())
		c.tcpConn.SetDeadline(time.Now().Add(c.tcpDeadline.altDuration))
		c.tcpDeadline.altDuration = 0
	} else {
		c.tcpConn.SetDeadline(time.Now().Add(c.tcpDeadline.stdDuration))
	}
}

// establishTCP attempts to connect to Calibre on a port previously obtained from Calibre
func (c *calConn) establishTCP() error {
	var err error
	// Connect to Calibre
	c.tcpConn, err = c.calibreInstance.Connect()
	if err != nil {
		return fmt.Errorf("establishTCP: %w", err)
	}
	c.setTCPDeadline()
	c.tcpReader = bufio.NewReader(c.tcpConn)
	return nil
}

// Convenience function to handle writing to our TCP connection, and manage the deadline
func (c *calConn) writeTCP(payload []byte) error {
	var terr net.Error
	_, err := c.tcpConn.Write(payload)
	if errors.As(err, &terr) && terr.Timeout() {
		return fmt.Errorf("writeTCP: connection timed out: %w", err)
	} else if err != nil {
		if err == io.EOF {
			return err
		}
		return fmt.Errorf("writeTCP: write to tcp connection failed: %w", err)
	}
	c.setTCPDeadline()
	c.LogPrintf("Wrote TCP packet: %.40s\n", string(payload))
	return nil
}

// readTCP reads and parses a Calibre packet from the TCP connection
func (c *calConn) readTCP() ([]byte, error) {
	var terr net.Error
	// Read Size of the payload. The payload looks like
	// 13[0,{"foo":1}]
	msgSz, err := c.tcpReader.ReadBytes('[')
	if errors.As(err, &terr) && terr.Timeout() {
		return nil, fmt.Errorf("readTCP: connection timed out: %w", err)
	}
	if err != nil {
		if err == io.EOF {
			return nil, err
		}
		return nil, fmt.Errorf("readTCP: ReadBytes failed: %w", err)
	}
	buffLen := len(msgSz)
	c.setTCPDeadline()
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
		return nil, fmt.Errorf("readTCP: error decoding payload size: %w", err)
	}
	// We have our payload size. Create the appropriate buffer.
	// and read into it.
	payload := make([]byte, sz)
	io.ReadFull(c.tcpReader, payload)
	if errors.As(err, &terr) && terr.Timeout() {
		return nil, fmt.Errorf("readTCP: connection timed out: %w", err)
	} else if err != nil {
		if err == io.EOF {
			return nil, err
		}
		return nil, fmt.Errorf("readTCP: did not receive full payload: %w", err)
	}
	c.setTCPDeadline()
	c.LogPrintf("Read TCP packet: %.40s\n", string(payload))
	return payload, nil
}

// handleNoop deals with calibre NOOP's
func (c *calConn) handleNoop(dataBytes json.RawMessage) error {
	var err error
	data := make(map[string]interface{})
	if err = json.Unmarshal(dataBytes, &data); err != nil {
		return fmt.Errorf("handleNoop: err decoding noop packet: %w", err)
	}
	// Calibre appears to use this opcode as a keep-alive signal
	// We reply to tell callibre is all still good.
	if len(data) == 0 {
		c.client.UpdateStatus(Idle, -1)
		err = c.writeTCP([]byte(c.okStr))
		if err != nil {
			return fmt.Errorf("handleNoop: %w", err)
		}
		// Calibre also uses noops to request more metadata from books
		// on device. We handle that case here.
	} else if val, exist := data["count"]; exist {
		count := int(val.(float64))
		// We don't do anything if count is zero
		if count == 0 {
			return nil
		}
		c.client.UpdateStatus(SendingExtraMetadata, -1)
		bookList := make([]BookID, count)
		for i := 0; i < count; i++ {
			opcode, newdata, err := c.readDecodeCalibrePayload()
			var pk struct {
				PriKey int `json:"priKey"`
			}
			if err = json.Unmarshal(newdata, &pk); err != nil {
				return fmt.Errorf("handleNoop: error getting primary key from calibre: %w", err)
			}
			if err != nil {
				if err == io.EOF {
					return err
				}
				return fmt.Errorf("handleNoop: packet reading failed: %w", err)
			}
			if opcode != noop {
				return fmt.Errorf("handleNoop: noop expected")
			}
			_, bd, err := c.ucdb.find(PriKey, pk.PriKey)
			if err != nil {
				return fmt.Errorf("handleNoop: %w", err)
			}
			bID := BookID{Lpath: bd.Lpath, UUID: bd.UUID}
			bookList[i] = bID
		}
		err := c.resendMetadataList(bookList)
		if err != nil {
			return fmt.Errorf("handleNoop: error resending metadata: %w", err)
		}
		// For any other message we don't yet know about, send an ok packet.
		// This fixes an issue of Calibre sending an unknown message and expecting some sort of response
	} else {
		c.client.UpdateStatus(Idle, -1)
		err = c.writeTCP([]byte(c.okStr))
		if err != nil {
			return fmt.Errorf("handleNoop: %w", err)
		}
	}
	return nil
}

// handleMessage deals with message packets from Calibre, instead of the normal
// opcode packets. We currently handle password error messages only.
func (c *calConn) handleMessage(data json.RawMessage) error {
	var err error
	var mk struct {
		MessageKind calMsgCode `json:"messageKind"`
	}
	if err = json.Unmarshal(data, &mk); err != nil {
		return fmt.Errorf("handleMessage: error getting message kind from calibre: %w", err)
	}
	switch mk.MessageKind {
	case passwordError:
		// Respond to calibre, then close the connection
		c.writeTCP([]byte(c.okStr))
		c.tcpConn.Close()
		// Ask the user for a password
		if c.serverPassword, err = c.client.GetPassword(c.calibreInfo); err != nil {
			return fmt.Errorf("handleMessage: error retrieving password: %w", err)
		}
		if c.serverPassword == "" {
			c.client.UpdateStatus(EmptyPasswordReceived, -1)
			return NoPassword
		}
		return c.establishTCP()
	}
	return err
}

// getInitInfo handles the request from Calibre to send initialization info.
func (c *calConn) getInitInfo(data json.RawMessage) error {
	if err := json.Unmarshal(data, &c.calibreInfo); err != nil {
		return fmt.Errorf("getInitInfo: error decoding calibre data: %w", err)
	}
	extPathLen := make(map[string]int)
	for _, e := range c.clientOpts.SupportedExt {
		extPathLen[e] = 38
	}
	// Note, the first time we are challenged with a password, we respond
	// with an incorrect password. This gives us the opportunity to close
	// the connection, and spend as long as we need to gather a password from
	// the client.
	passHash := ""
	if c.calibreInfo.PasswordChallenge != "" {
		passHash = c.hashCalPassword(c.calibreInfo.PasswordChallenge)
	}
	initInfo := CalibreInit{
		VersionOK:               true,
		MaxBookContentPacketLen: bookPacketContentLen,
		AcceptedExtensions:      c.clientOpts.SupportedExt,
		ExtensionPathLengths:    extPathLen,
		PasswordHash:            passHash,
		CcVersionNumber:         391,
		CanStreamBooks:          true,
		CanStreamMetadata:       true,
		CanReceiveBookBinary:    true,
		CanDeleteMultipleBooks:  true,
		CanUseCachedMetadata:    true,
		DeviceKind:              c.deviceInfo.DeviceVersion,
		DeviceName:              c.deviceInfo.DevInfo.DeviceName,
		CoverHeight:             c.clientOpts.CoverDims.Height,
		AppName:                 c.clientOpts.ClientName,
		CacheUsesLpaths:         true,
		CanSendOkToSendbook:     true,
		CanAcceptLibraryInfo:    true,
	}
	payload := buildJSONpayload(initInfo, ok)
	return c.writeTCP(payload)
}

// getDeviceInfo handles the request from Calibre for the device (that's us!)
// to send information about itself
func (c *calConn) getDeviceInfo() error {
	// By this point, we should have an initial connection to calibre
	c.client.UpdateStatus(Connected, -1)
	c.deviceInfo.DeviceVersion = c.clientOpts.DeviceModel
	c.deviceInfo.Version = "391"
	payload := buildJSONpayload(c.deviceInfo, ok)
	return c.writeTCP(payload)
}

// setDeviceInfo saves the return information we got from Calibre
// to place in the '.driveinfo.calibre' file
func (c *calConn) setDeviceInfo(data json.RawMessage) error {
	var devInfo DeviceInfo
	if err := json.Unmarshal(data, &devInfo.DevInfo); err != nil {
		return fmt.Errorf("setDeviceInfo: error decoding data: %w", err)
	}
	c.client.SetDeviceInfo(devInfo)
	return c.writeTCP([]byte(c.okStr))
}

// getFreeSpace tells Calibre how much space is available in our
// book directory.
func (c *calConn) getFreeSpace() error {
	var space FreeSpace
	space.FreeSpaceOnDevice = c.client.GetFreeSpace()
	payload := buildJSONpayload(space, ok)
	return c.writeTCP(payload)
}

// getBookCount sends Calibre a list of ebooks currently on the device.
// It is up to the client to decide how this list is derived
func (c *calConn) getBookCount(data json.RawMessage) error {
	var err error
	var bcOpts BookCountReceive
	if err = json.Unmarshal(data, &bcOpts); err != nil {
		return fmt.Errorf("getBookCount: error decoding options: %w", err)
	}
	len := c.ucdb.length()
	bc := BookCountSend{Count: len, WillStream: true, WillScan: true}
	// when setting "willUseCachedMetadata" to true, Calibre is expecting a list
	// of books with abridged metadata (the contents of the bookCountDetails struct)
	if bcOpts.WillUseCachedMetadata {
		payload := buildJSONpayload(bc, ok)
		// Send our count
		if err = c.writeTCP(payload); err != nil {
			return fmt.Errorf("getBookCount: error sending count: %w", err)
		}

		for _, b := range c.ucdb.booklist {
			payload = buildJSONpayload(b, ok)
			if err = c.writeTCP(payload); err != nil {
				return fmt.Errorf("getBookCount: error sending bookCountDetail: %w", err)
			}
		}
		// Otherwise, Calibre expects a full set of metadata for each book on the
		// device. We get that from the client.
	} else {
		mdIter := c.client.GetMetadataIter([]BookID{})
		bc.Count = mdIter.Count()
		payload := buildJSONpayload(bc, ok)
		// Send our count
		if err = c.writeTCP(payload); err != nil {
			return fmt.Errorf("getBookCount: error sending count: %w", err)
		}
		for mdIter.Next() {
			md, err := mdIter.Get()
			if err != nil {
				return fmt.Errorf("getBookCount: error retrieving book metadata: %w", err)
			}
			// Ensure maps are empty, not nil
			md.InitMaps()
			payload := buildJSONpayload(md, ok)
			if err = c.writeTCP(payload); err != nil {
				return fmt.Errorf("getBookCount: error sending book metadata: %w", err)
			}
		}
	}
	// Calibre can take a while to process large book lists (hundreds to thousands of books)
	// So we increase the connection deadline to something reasonable.
	c.tcpDeadline.altDuration = 300 * time.Second
	c.setTCPDeadline()
	c.client.UpdateStatus(Waiting, -1)
	return nil
}

// resendMetadataList is called whenever using cached metadata, and
// Calibre requests a complete metadata listing (eg, when using a
// different Calibre library)
func (c *calConn) resendMetadataList(bookList []BookID) error {
	mdIter := c.client.GetMetadataIter(bookList)
	if mdIter.Count() == 0 {
		return c.writeTCP([]byte(c.okStr))
	}
	for mdIter.Next() {
		md, err := mdIter.Get()
		if err != nil {
			return fmt.Errorf("resendMetadataList: error retrieving book metadata: %w", err)
		}
		// Ensure maps are empty, not nil
		md.InitMaps()
		payload := buildJSONpayload(md, ok)
		if err = c.writeTCP(payload); err != nil {
			return fmt.Errorf("resendMetadataList: error sending book metadata: %w", err)
		}
	}
	c.tcpDeadline.altDuration = 300 * time.Second
	c.setTCPDeadline()
	c.client.UpdateStatus(Waiting, -1)
	return nil
}

// updateDeviceMetadata recieves updated metadata from Calibre, and
// sends it to the client for updating
func (c *calConn) updateDeviceMetadata(data json.RawMessage) error {
	var err error
	var bld BookListsDetails
	if err = json.Unmarshal(data, &bld); err != nil {
		return fmt.Errorf("updateDeviceMetadata: error receiving count: %w", err)
	}
	// Double check that there will be new metadata incoming
	if bld.Count == 0 {
		return nil
	}
	// We read exactly 'count' metadata packets
	md := make([]CalibreBookMeta, bld.Count)
	for i := 0; i < bld.Count; i++ {
		var bkMD MetadataUpdate
		opcode, newdata, err := c.readDecodeCalibrePayload()
		if err != nil {
			if err == io.EOF {
				return err
			}
			return fmt.Errorf("updateDeviceMetadata: packet reading failed: %w", err)
		}

		// Opcode should be SEND_BOOK_METADATA. If it's not, something
		// has gone rather wrong
		if opcode != sendBookMetadata {
			return fmt.Errorf("updateDeviceMetadata: unexpected calibre packet type")
		}
		if err = json.Unmarshal(newdata, &bkMD); err != nil {
			return fmt.Errorf("updateDeviceMetadata: unable to decode metadata packet: %w", err)
		}
		md[i] = bkMD.Data
	}
	c.client.UpdateMetadata(md)
	return nil
}

func (c *calConn) setLibraryInfo(data json.RawMessage) (err error) {
	var libInfo CalibreLibraryInfo
	if err = json.Unmarshal(data, &libInfo); err != nil {
		return fmt.Errorf("setLibraryInfo: error decoding library info: %w", err)
	}
	if err = c.client.SetLibraryInfo(libInfo); err != nil {
		return fmt.Errorf("setLibraryInfo: client error while sending library info: %w", err)
	}
	return c.writeTCP([]byte(c.okStr))
}

// sendBook is where the magic starts to happen. It recieves one
// or more books from calibre.
func (c *calConn) sendBook(data json.RawMessage) (err error) {
	var bookDet SendBook
	if err = json.Unmarshal(data, &bookDet); err != nil {
		return fmt.Errorf("sendBook: error decoding book details: %w", err)
	}
	c.LogPrintf("Send Book detail is: %+v\n", bookDet)
	if bookDet.ThisBook == 0 {
		c.client.UpdateStatus(ReceivingBook, 0)
	}
	lastBook := false
	if bookDet.ThisBook == (bookDet.TotalBooks - 1) {
		lastBook = true
	}
	newLpath := c.client.CheckLpath(bookDet.Lpath)
	if bookDet.WantsSendOkToSendbook {
		c.LogPrintf("Sending OK-to-send packet\n")
		if bookDet.CanSupportLpathChanges && newLpath != bookDet.Lpath {
			bookDet.Lpath = newLpath
			bookDet.Metadata.Lpath = newLpath
			newLP := NewLpath{Lpath: bookDet.Lpath}
			payload := buildJSONpayload(newLP, ok)
			if err = c.writeTCP(payload); err != nil {
				return fmt.Errorf("sendBook: error writing OK-to-send packet: %w", err)
			}
		} else {
			if err = c.writeTCP([]byte(c.okStr)); err != nil {
				return fmt.Errorf("sendBook: error writing ok string: %w", err)
			}
		}
	}
	// we need to give the client time to download and process the book. Let's be pessimistic and assume
	// the process happens at 100KB/s
	c.tcpDeadline.altDuration = time.Duration(int(float64(bookDet.Length)/float64(102400)+1)*2) * time.Second
	c.setTCPDeadline()
	if err = c.client.SaveBook(bookDet.Metadata, c.tcpReader, bookDet.Length, lastBook); err != nil {
		return fmt.Errorf("sendBook: client error saving book: %w", err)
	}
	c.setTCPDeadline()
	c.ucdb.addEntry(bookDet.Metadata)
	progress := ((bookDet.ThisBook + 1) * 100) / bookDet.TotalBooks
	c.client.UpdateStatus(ReceivingBook, progress)
	return nil
}

// deleteBook will delete any ebook Calibre tells us to
func (c *calConn) deleteBook(data json.RawMessage) error {
	var err error
	if err = c.writeTCP([]byte(c.okStr)); err != nil {
		return fmt.Errorf("deleteBook: error writing ok string: %w", err)
	}
	var delBooks DeleteBooks
	if err = json.Unmarshal(data, &delBooks); err != nil {
		return fmt.Errorf("deleteBook: error decoding delbooks: %w", err)
	}
	c.client.UpdateStatus(DeletingBook, 0)
	for i, lp := range delBooks.Lpaths {
		_, bd, err := c.ucdb.find(Lpath, lp)
		if err != nil {
			return fmt.Errorf("deleteBook: lpath not in db to delete")
		}
		bID := BookID{Lpath: bd.Lpath, UUID: bd.UUID}
		if err = c.client.DeleteBook(bID); err != nil {
			return fmt.Errorf("deleteBook: client error deleting book: %w", err)
		}
		payload := buildJSONpayload(map[string]string{"uuid": bd.UUID}, ok)
		c.writeTCP(payload)
		c.ucdb.removeEntry(Lpath, lp)
		progress := ((i + 1) * 100) / len(delBooks.Lpaths)
		c.client.UpdateStatus(DeletingBook, progress)
	}
	return nil
}

// getBook will send the ebook requested by Calibre, to calibre
func (c *calConn) getBook(data json.RawMessage) error {
	var err error
	var gbr GetBookReceive
	if err = json.Unmarshal(data, &gbr); err != nil {
		return fmt.Errorf("getBook: error decoding calibre settings")
	}
	c.client.UpdateStatus(SendingBook, -1)
	if !gbr.CanStreamBinary || !gbr.CanStream {
		return fmt.Errorf("getBook: calibre version does not support binary streaming")
	}
	_, bd, err := c.ucdb.find(Lpath, gbr.Lpath)
	if err != nil {
		return fmt.Errorf("getBook: could not get book from db: %w", err)
	}
	bID := BookID{Lpath: gbr.Lpath, UUID: bd.UUID}
	bk, len, err := c.client.GetBook(bID, gbr.Position)
	if err != nil {
		return fmt.Errorf("getBook: could not open book file: %w", err)
	}
	gb := GetBookSend{
		WillStream:       true,
		WillStreamBinary: true,
		FileLength:       len,
	}
	payload := buildJSONpayload(gb, ok)
	if err = c.writeTCP(payload); err != nil {
		return fmt.Errorf("getBook: error writing GetBook payload: %w", err)
	}
	// we need to make sure the TCP connection doesn't timeout for large books
	// Let's be pessimistic and assume the process happens at 100KB/s
	c.tcpDeadline.altDuration = time.Duration(int(float64(len)/float64(102400)+1)*2) * time.Second
	c.setTCPDeadline()
	if _, err = io.CopyN(c.tcpConn, bk, len); err != nil {
		bk.Close()
		return fmt.Errorf("getBook: error sending book to Calibre: %w", err)
	}
	bk.Close()
	c.setTCPDeadline()
	return nil
}
