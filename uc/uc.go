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
	"strings"
	"sync"
	"time"

	"github.com/mitchellh/mapstructure"
)

const tcpDeadlineTimeout = 15
const bookPacketContentLen = 4096

// buildJSONpayload builds a payload in the format that Calibre expects
func buildJSONpayload(jsonBytes []byte, op calOpCode) []byte {
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
	c.ucdb = &UncagedDB{}
	bookList, retErr := c.client.GetDeviceBookList()
	if retErr != nil {
		return nil, fmt.Errorf("New: Error getting booklist from device: %w", retErr)
	}
	c.ucdb.initDB(bookList)
	if c.deviceInfo, retErr = c.client.GetDeviceInfo(); retErr != nil {
		return nil, fmt.Errorf("New: Error getting info from device: %w", retErr)
	}
	// Calibre listens for a 'hello' UDP packet on the following
	// five ports. We try all five ports concurrently
	c.client.UpdateStatus(SearchingCalibre, -1)
	bcastPorts := []int{54982, 48123, 39001, 44044, 59678}
	wg := &sync.WaitGroup{}
	mu := &sync.Mutex{}
	for _, p := range bcastPorts {
		wg.Add(1)
		go c.findCalibre(p, mu, wg)
	}
	wg.Wait()
	if len(c.calibreInstances) == 0 {
		return nil, fmt.Errorf("New: Could not find calibre instance: %w", CalibreNotFound)
	}
	c.calibreAddr = c.client.SelectCalibreInstance(c.calibreInstances).Addr
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
func (ucdb *UncagedDB) addEntry(md map[string]interface{}) error {
	// mapstructure.Decode() does not decode time (in strings) to time.Time, hence the need
	// to create a decoder config and decoder, using a provided hook.
	var bd BookCountDetails
	config := mapstructure.DecoderConfig{
		DecodeHook: mapstructure.StringToTimeHookFunc(time.RFC3339),
		Result:     &bd,
	}
	decoder, err := mapstructure.NewDecoder(&config)
	if err != nil {
		return fmt.Errorf("addEntry: could not create decoder: %w", err)
	}
	decoder.Decode(md)
	if err != nil {
		return fmt.Errorf("addEntry: could not decode metadata: %w", err)
	}
	bd.PriKey = ucdb.newPriKey()
	ucdb.booklist = append(ucdb.booklist, bd)
	return nil
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
	c.client.UpdateStatus(Connecting, -1)
	err = c.establishTCP()
	if err != nil {
		return fmt.Errorf("Start: establishing connection failed: %w", err)
	}
	// Connect to Calibre
	// Keep reading untill the connection is closed
	for {
		opcode, data, err := c.readDecodeCalibrePayload()
		if err != nil {
			if err == io.EOF {
				c.debugLogPrintf("TCP Connection Closed")
				return nil
			}
			return fmt.Errorf("Start: packet reading failed: %w", err)
		}
		c.debugLogPrintf("Calibre Opcode received: %v\n", opcode)
		switch opcode {
		case getInitializationInfo:
			err = c.getInitInfo(data)
		case displayMessage:
			err = c.handleMessage(data)
		case getDeviceInformation:
			err = c.getDeviceInfo(data)
		case setCalibreDeviceInfo:
			err = c.setDeviceInfo(data)
		case freeSpace:
			err = c.getFreeSpace()
		case getBookCount:
			err = c.getBookCount(data)
		case sendBooklists:
			err = c.updateDeviceMetadata(data)
		case setLibraryInfo:
			err = c.writeTCP([]byte(c.okStr))
		case sendBook:
			err = c.sendBook(data)
		case deleteBook:
			err = c.deleteBook(data)
		case getBookFileSegment:
			err = c.getBook(data)
		case noop:
			err = c.handleNoop(data)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("Start: exiting with error: %w", err)
		}
	}
}

func (c *calConn) debugLogPrintf(format string, a ...interface{}) {
	if c.debug {
		c.client.LogPrintf(Debug, format, a...)
	}
}

func (c *calConn) decodeCalibrePayload(payload []byte) (calOpCode, map[string]interface{}, error) {
	var calibreDat []interface{}
	if err := json.Unmarshal(payload, &calibreDat); err != nil {
		return -1, nil, fmt.Errorf("decodeCalibrePayload: could not unmarshal payload: %w", err)
	}
	// The first element should always be an opcode
	opcode := calOpCode(calibreDat[0].(float64))
	value := calibreDat[1].(map[string]interface{})
	return opcode, value, nil
}

func (c *calConn) readDecodeCalibrePayload() (calOpCode, map[string]interface{}, error) {
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

// establishTCP attempts to connect to Calibre on a port previously obtained from Calibre
func (c *calConn) establishTCP() error {
	err := error(nil)
	// Connect to Calibre
	c.tcpConn, err = net.Dial("tcp", c.calibreAddr)
	if err != nil {
		return fmt.Errorf("establishTCP: dialing calibre failed: %w", err)
	}
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
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
		return fmt.Errorf("writeTCP: write to tcp connection failed: %w", err)
	}
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
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
		return nil, fmt.Errorf("readTCP: ReadBytes failed: %w", err)
	}
	buffLen := len(msgSz)
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
		return nil, fmt.Errorf("readTCP: error decoding payload size: %w", err)
	}
	// We have our payload size. Create the appropriate buffer.
	// and read into it.
	payload := make([]byte, sz)
	io.ReadFull(c.tcpReader, payload)
	if errors.As(err, &terr) && terr.Timeout() {
		return nil, fmt.Errorf("readTCP: connection timed out: %w", err)
	} else if err != nil {
		return nil, fmt.Errorf("readTCP: did not receive full payload: %w", err)
	}
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
	return payload, nil
}

// handleNoop deals with calibre NOOP's
func (c *calConn) handleNoop(data map[string]interface{}) error {
	// Calibre appears to use this opcode as a keep-alive signal
	// We reply to tell callibre is all still good.
	if len(data) == 0 {
		c.client.UpdateStatus(Idle, -1)
		err := c.writeTCP([]byte(c.okStr))
		if err != nil {
			return fmt.Errorf("handleNoop: %w", err)
		}
		// Calibre also uses noops to request more metadata from books
		// on device. We handle that case here.
	} else {
		count := 0
		if val, exist := data["count"]; exist {
			count = int(val.(float64))
		}
		// We don't do anything if count is zero
		if count == 0 {
			return nil
		}
		c.client.UpdateStatus(SendingExtraMetadata, -1)
		bookList := make([]BookID, count)
		for i := 0; i < count; i++ {
			opcode, newdata, err := c.readDecodeCalibrePayload()
			if err != nil {
				if err == io.EOF {
					return err
				}
				return fmt.Errorf("handleNoop: packet reading failed: %w", err)
			}
			if opcode != noop {
				return fmt.Errorf("handleNoop: noop expected")
			}
			_, bd, err := c.ucdb.find(PriKey, int(newdata["priKey"].(float64)))
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
	}
	return nil
}

// handleMessage deals with message packets from Calibre, instead of the normal
// opcode packets. We currently handle password error messages only.
func (c *calConn) handleMessage(data map[string]interface{}) error {
	var err error
	msgType := calMsgCode(data["messageKind"].(float64))
	switch msgType {
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
func (c *calConn) getInitInfo(data map[string]interface{}) error {
	err := mapstructure.Decode(data, &c.calibreInfo)
	if err != nil {
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
		CanAcceptLibraryInfo:    false,
	}
	initJSON, _ := json.Marshal(initInfo)
	payload := buildJSONpayload(initJSON, ok)
	return c.writeTCP(payload)
}

// getDeviceInfo handles the request from Calibre for the device (that's us!)
// to send information about itself
func (c *calConn) getDeviceInfo(data map[string]interface{}) error {
	// By this point, we should have an initial connection to calibre
	c.client.UpdateStatus(Connected, -1)
	c.deviceInfo.DeviceVersion = c.clientOpts.DeviceModel
	c.deviceInfo.Version = "391"
	devInfoJSON, _ := json.Marshal(c.deviceInfo)
	payload := buildJSONpayload(devInfoJSON, ok)
	return c.writeTCP(payload)
}

// setDeviceInfo saves the return information we got from Calibre
// to place in the '.driveinfo.calibre' file
func (c *calConn) setDeviceInfo(data map[string]interface{}) error {
	var devInfo DeviceInfo
	config := mapstructure.DecoderConfig{
		DecodeHook: mapstructure.StringToTimeHookFunc(time.RFC3339),
		Result:     &devInfo.DevInfo,
	}
	decoder, err := mapstructure.NewDecoder(&config)
	if err != nil {
		return fmt.Errorf("setDeviceInfo: error creating ms decoder: %w", err)
	}
	decoder.Decode(data)
	if err != nil {
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
	fsJSON, _ := json.Marshal(space)
	payload := buildJSONpayload(fsJSON, ok)
	return c.writeTCP(payload)
}

// getBookCount sends Calibre a list of ebooks currently on the device.
// It is up to the client to decide how this list is derived
func (c *calConn) getBookCount(data map[string]interface{}) error {
	var err error
	bc := BookCount{Count: c.ucdb.length(), WillStream: true, WillScan: true}
	// when setting "willUseCachedMetadata" to true, Calibre is expecting a list
	// of books with abridged metadata (the contents of the bookCountDetails struct)
	if data["willUseCachedMetadata"].(bool) {
		bcJSON, _ := json.Marshal(bc)
		payload := buildJSONpayload(bcJSON, ok)
		// Send our count
		if err = c.writeTCP(payload); err != nil {
			return fmt.Errorf("getBookCount: error sending count: %w", err)
		}

		for _, b := range c.ucdb.booklist {
			bJSON, _ := json.Marshal(b)
			payload = buildJSONpayload(bJSON, ok)
			if err = c.writeTCP(payload); err != nil {
				return fmt.Errorf("getBookCount: error sending bookCountDetail: %w", err)
			}
		}
		// Otherwise, Calibre expects a full set of metadata for each book on the
		// device. We get that from the client.
	} else {
		md, err := c.client.GetMetadataList([]BookID{})
		if err != nil {
			return fmt.Errorf("getBookCount: error getting metadata from device: %w", err)
		}
		bc.Count = len(md)
		bcJSON, _ := json.Marshal(bc)
		payload := buildJSONpayload(bcJSON, ok)
		// Send our count
		if err = c.writeTCP(payload); err != nil {
			return fmt.Errorf("getBookCount: error sending count: %w", err)
		}
		for _, m := range md {
			mJSON, _ := json.Marshal(m)
			payload := buildJSONpayload(mJSON, ok)
			if err = c.writeTCP(payload); err != nil {
				return fmt.Errorf("getBookCount: error sending book metadata: %w", err)
			}
		}
	}
	return nil
}

// resendMetadataList is called whenever using cached metadata, and
// Calibre requests a complete metadata listing (eg, when using a
// different Calibre library)
func (c *calConn) resendMetadataList(bookList []BookID) error {
	mdList, err := c.client.GetMetadataList(bookList)
	if err != nil {
		return fmt.Errorf("resendMetadataList: error getting metadata from device: %w", err)
	}
	if len(mdList) == 0 {
		return c.writeTCP([]byte(c.okStr))
	}
	for _, md := range mdList {
		mJSON, _ := json.Marshal(md)
		payload := buildJSONpayload(mJSON, ok)
		if err = c.writeTCP(payload); err != nil {
			return fmt.Errorf("resendMetadataList: error sending book metadata: %w", err)
		}
	}
	return nil
}

// updateDeviceMetadata recieves updated metadata from Calibre, and
// sends it to the client for updating
func (c *calConn) updateDeviceMetadata(data map[string]interface{}) error {
	// Double check that there will be new metadata incoming
	if data["count"].(float64) == 0 {
		return nil
	}
	// We read exactly 'count' metadata packets
	count := int(data["count"].(float64))
	md := make([]map[string]interface{}, count)
	for i := 0; i < count; i++ {
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
		if err = mapstructure.Decode(newdata, &bkMD); err != nil {
			return fmt.Errorf("updateDeviceMetadata: unable to decode metadata packet: %w", err)
		}
		md[i] = bkMD.Data
	}
	c.client.UpdateMetadata(md)
	return nil
}

// sendBook is where the magic starts to happen. It recieves one
// or more books from calibre.
func (c *calConn) sendBook(data map[string]interface{}) (err error) {
	var bookDet SendBook
	if err = mapstructure.Decode(data, &bookDet); err != nil {
		return fmt.Errorf("sendBook: error decoding book details: %w", err)
	}
	c.debugLogPrintf("Send Book detail is: %+v\n", bookDet)
	if bookDet.ThisBook == 0 {
		c.client.UpdateStatus(ReceivingBook, 0)
	}
	lastBook := false
	if bookDet.ThisBook == (bookDet.TotalBooks - 1) {
		lastBook = true
	}
	newLpath := c.client.CheckLpath(bookDet.Lpath)
	if bookDet.WantsSendOkToSendbook {
		c.debugLogPrintf("Sending OK-to-send packet\n")
		if bookDet.CanSupportLpathChanges && newLpath != bookDet.Lpath {
			bookDet.Lpath = newLpath
			bookDet.Metadata["lpath"] = newLpath
			newLP := NewLpath{Lpath: bookDet.Lpath}
			lpJSON, _ := json.Marshal(newLP)
			payload := buildJSONpayload(lpJSON, ok)
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
	saveTimeout := time.Duration(int(float64(bookDet.Length)/float64(102400)+1) * 2)
	c.tcpConn.SetDeadline(time.Now().Add(saveTimeout * time.Second))
	if err = c.client.SaveBook(bookDet.Metadata, c.tcpReader, bookDet.Length, lastBook); err != nil {
		return fmt.Errorf("sendBook: client error saving book: %w", err)
	}
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
	c.ucdb.addEntry(bookDet.Metadata)
	progress := ((bookDet.ThisBook + 1) * 100) / bookDet.TotalBooks
	c.client.UpdateStatus(ReceivingBook, progress)
	return nil
}

// deleteBook will delete any ebook Calibre tells us to
func (c *calConn) deleteBook(data map[string]interface{}) error {
	var err error
	if err = c.writeTCP([]byte(c.okStr)); err != nil {
		return fmt.Errorf("deleteBook: error writing ok string: %w", err)
	}
	var delBooks DeleteBooks
	mapstructure.Decode(data, &delBooks)
	for _, lp := range delBooks.Lpaths {
		_, bd, err := c.ucdb.find(Lpath, lp)
		if err != nil {
			return fmt.Errorf("deleteBook: lpath not in db to delete")
		}
		bID := BookID{Lpath: bd.Lpath, UUID: bd.UUID}
		if err = c.client.DeleteBook(bID); err != nil {
			return fmt.Errorf("deleteBook: client error deleting book: %w", err)
		}
		calConfirm, _ := json.Marshal(map[string]string{"uuid": bd.UUID})
		payload := buildJSONpayload(calConfirm, ok)
		c.writeTCP(payload)
		c.ucdb.removeEntry(Lpath, lp)
	}
	return nil
}

// getBook will send the ebook requested by Calibre, to calibre
func (c *calConn) getBook(data map[string]interface{}) error {
	c.client.UpdateStatus(SendingBook, -1)
	if !data["canStreamBinary"].(bool) || !data["canStream"].(bool) {
		return fmt.Errorf("getBook: calibre version does not support binary streaming")
	}
	lpath := data["lpath"].(string)
	filePos := int64(data["position"].(float64))
	_, bd, err := c.ucdb.find(Lpath, lpath)
	if err != nil {
		return fmt.Errorf("getBook: could not get book from db: %w", err)
	}
	bID := BookID{Lpath: lpath, UUID: bd.UUID}
	bk, len, err := c.client.GetBook(bID, filePos)
	if err != nil {
		return fmt.Errorf("getBook: could not open book file: %w", err)
	}
	gb := GetBook{
		WillStream:       true,
		WillStreamBinary: true,
		FileLength:       len,
	}
	gbJSON, _ := json.Marshal(gb)
	payload := buildJSONpayload(gbJSON, ok)
	if err = c.writeTCP(payload); err != nil {
		return fmt.Errorf("getBook: error writing GetBook payload: %w", err)
	}
	// we need to make sure the TCP connection doesn't timeout for large books
	// Let's be pessimistic and assume the process happens at 100KB/s
	sendTimeout := time.Duration(int(float64(len)/float64(102400)+1) * 2)
	c.tcpConn.SetDeadline(time.Now().Add(sendTimeout * time.Second))
	if _, err = io.CopyN(c.tcpConn, bk, len); err != nil {
		bk.Close()
		return fmt.Errorf("getBook: error sending book to Calibre: %w", err)
	}
	bk.Close()
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
	return nil
}

// findCalibre performs the original search for a Calibre instance, using
// UDP. Note, if there are multple calibre instances with their wireless
// connection active, we select the first that responds.
func (c *calConn) findCalibre(bcastPort int, mu *sync.Mutex, wg *sync.WaitGroup) {
	defer wg.Done()
	localAddress := "0.0.0.0:0"
	portStr := fmt.Sprintf("%d", bcastPort)
	bcastAddress := "255.255.255.255:" + portStr
	pc, err := net.ListenPacket("udp", localAddress)
	if err != nil {
		c.client.LogPrintf(Info, "%v\n", err)
	}
	defer pc.Close()
	calibreReply := make([]byte, 256)
	udpAddr, _ := net.ResolveUDPAddr("udp", bcastAddress)
	pc.WriteTo([]byte("hello"), udpAddr)
	deadlineTime := time.Now().Add(1 * time.Second)
	pc.SetReadDeadline(deadlineTime)
	for {
		bytesRead, addr, err := pc.ReadFrom(calibreReply)
		if e, ok := err.(net.Error); ok && e.Timeout() {
			pc.Close()
			return
		} else if err != nil {
			c.client.LogPrintf(Info, "%v\n", err)
			return
		}
		calibreIP, _, _ := net.SplitHostPort(addr.String())
		calibreMsg := string(calibreReply[:bytesRead])
		msgData := strings.Split(calibreMsg, ",")
		calibrePort := msgData[len(msgData)-1]
		instance := CalInstance{Addr: calibreIP + ":" + calibrePort, Description: msgData[0]}
		mu.Lock()
		c.calibreInstances = append(c.calibreInstances, instance)
		mu.Unlock()
	}
}
