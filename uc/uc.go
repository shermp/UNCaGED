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
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
)

const tcpDeadlineTimeout = 15
const bookPacketContentLen = 4096

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

// New initilizes the calibre connection, and returns it
// An error is returned if a Calibre instance cannot be found
func New(client Client) (*calConn, error) {
	var retErr error
	retErr = nil
	c := &calConn{}
	c.client = client
	c.clientOpts = c.client.GetClientOptions()
	c.transferCount = 0
	c.okStr = "6[0,{}]"
	if retErr != nil {
		return nil, retErr
	}
	c.ucdb = &UncagedDB{}
	c.ucdb.initDB(c.client.GetDeviceBookList())
	c.deviceInfo = c.client.GetDeviceInfo()
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
	err = errors.New("no match")
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
			err = errors.New("invalid type. Expecting integer")
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
			err = errors.New("invalid type. Expecting string")
		}
	}
	return index, bd, err
}

func (ucdb *UncagedDB) length() int {
	return len(ucdb.booklist)
}

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
		return err
	}
	decoder.Decode(md)
	if err != nil {
		return errors.Wrap(err, "could not decode metadata")
	}
	bd.PriKey = ucdb.newPriKey()
	ucdb.booklist = append(ucdb.booklist, bd)
	return nil
}

func (ucdb *UncagedDB) removeEntry(searchType ucdbSearchType, value interface{}) error {
	index, _, err := ucdb.find(searchType, value)
	if err != nil {
		return errors.Wrap(err, "search failed")
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
	err = c.establishTCP()
	if err != nil {
		return errors.Wrap(err, "establishing connection failed")
	}

	// Connect to Calibre
	// Keep reading untill the connection is closed
	for {
		opcode, data, err := c.readDecodeCalibrePayload()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return errors.Wrap(err, "packet reading failed")
		}

		switch opcode {
		case GET_INITIALIZATION_INFO:
			err = c.getInitInfo(data)
		case DISPLAY_MESSAGE:
			err = c.handleMessage(data)
		case GET_DEVICE_INFORMATION:
			err = c.getDeviceInfo(data)
		case SET_CALIBRE_DEVICE_INFO:
			err = c.setDeviceInfo(data)
		case FREE_SPACE:
			err = c.getFreeSpace()
		case GET_BOOK_COUNT:
			err = c.getBookCount(data)
		case SEND_BOOKLISTS:
			err = c.updateDeviceMetadata(data)
		case SET_LIBRARY_INFO:
			err = c.writeTCP([]byte(c.okStr))
		case SEND_BOOK:
			err = c.sendBook(data)
		case DELETE_BOOK:
			err = c.deleteBook(data)
		case GET_BOOK_FILE_SEGMENT:
			err = c.getBook(data)
		case NOOP:
			err = c.handleNoop(data)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
	return nil
}

func (c *calConn) decodeCalibrePayload(payload []byte) (calOpCode, map[string]interface{}, error) {
	var calibreDat []interface{}
	err := json.Unmarshal(payload, &calibreDat)
	if err != nil {
		return -1, nil, errors.Wrap(err, "could not unmarshal payload")
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
			c.client.Println("Calibre Disconnected")
			return NOOP, nil, err
		}
		return NOOP, nil, errors.Wrap(err, "connection closed")
	}
	opcode, data, err := c.decodeCalibrePayload(payload)
	if err != nil {
		return NOOP, nil, errors.Wrap(err, "packet decoding failed")
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

// Write the current metadata to a file on disk
// func (c *calConn) writeCurrentMetadata() error {
// 	mdPath := filepath.Join(c.clientOpts.DevStore.BookDir, ".metadata.calibre")
// 	mdJSON, _ := json.MarshalIndent(c.metadata, "", "    ")
// 	err := ioutil.WriteFile(mdPath, mdJSON, 0644)
// 	if err != nil {
// 		return errors.Wrap(err, "failed writing metadata")
// 	}
// 	return nil
// }

// Convenience function to handle writing to our TCP connection, and manage the deadline

func (c *calConn) establishTCP() error {
	err := error(nil)
	c.client.Println("Connecting to Calibre...")
	// Connect to Calibre
	c.tcpConn, err = net.Dial("tcp", c.calibreAddr)
	if err != nil {
		return errors.Wrap(err, "calibre connection failed")
	}
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
	c.client.Println("Connected to Calibre!")
	c.tcpReader = bufio.NewReader(c.tcpConn)
	return nil
}
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

func (c *calConn) readTCP() ([]byte, error) {
	// Read Size of the payload. The payload looks like
	// 13[0,{"foo":1}]
	msgSz, err := c.tcpReader.ReadBytes('[')
	buffLen := len(msgSz)
	if e, ok := err.(net.Error); ok && e.Timeout() {
		return nil, errors.Wrap(err, "connection timed out!")
	}
	if err != nil {
		return nil, err
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
		return nil, errors.Wrap(err, "error decoding payload size")
	}
	// We have our payload size. Create the appropriate buffer.
	// and read into it.
	payload := make([]byte, sz)
	io.ReadFull(c.tcpReader, payload)
	if e, ok := err.(net.Error); ok && e.Timeout() {
		return nil, errors.Wrap(err, "connection timed out!")
	} else if err != nil {
		return nil, errors.Wrap(err, "did not receive full payload")
	}
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
	return payload, nil
}

func (c *calConn) handleNoop(data map[string]interface{}) error {
	if len(data) == 0 {
		// Calibre appears to use this opcode as a keep-alive signal
		// We reply to tell callibre is all still good.
		err := c.writeTCP([]byte(c.okStr))
		if err != nil {
			return err
		}
	} else {
		count := 0
		if val, exist := data["count"]; exist {
			count = int(val.(float64))
		}
		// We don't do anything if count is zero
		if count == 0 {
			return nil
		}
		bookList := make([]BookID, count)
		for i := 0; i < count; i++ {
			opcode, newdata, err := c.readDecodeCalibrePayload()
			if err != nil {
				if err == io.EOF {
					return err
				}
				return errors.Wrap(err, "packet reading failed")
			}
			if opcode != NOOP {
				return errors.New("noop expected")
			}
			_, bd, err := c.ucdb.find(PriKey, int(newdata["priKey"].(float64)))
			if err != nil {
				return errors.Wrap(err, "could not find book in db")
			}
			bID := BookID{Lpath: bd.Lpath, UUID: bd.UUID}
			bookList[i] = bID
		}
		err := c.resendMetadataList(bookList)
		if err != nil {
			return errors.Wrap(err, "error resending metadata")
		}
	}
	return nil
}

func (c *calConn) handleMessage(data map[string]interface{}) error {
	msgType := calMsgCode(data["messageKind"].(float64))
	switch msgType {
	case MESSAGE_PASSWORD_ERROR:
		// Respond to calibre, then close the connection
		c.writeTCP([]byte(c.okStr))
		c.tcpConn.Close()
		// Ask the user for a password
		c.serverPassword = c.client.GetPassword()
		if c.serverPassword == "" {
			return errors.New("no password entered")
		}
		return c.establishTCP()
	}
	return nil
}

// getInitInfo handles the request from Calibre to send initialization info.
func (c *calConn) getInitInfo(data map[string]interface{}) error {
	extPathLen := make(map[string]int)
	for _, e := range c.clientOpts.SupportedExt {
		extPathLen[e] = 38
	}
	passHash := ""
	if c.serverPassword != "" && data["passwordChallenge"].(string) != "" {
		passHash = c.hashCalPassword(data["passwordChallenge"].(string))
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
	initJSON, _ := json.Marshal(initInfo)
	payload := buildJSONpayload(initJSON, OK)
	return c.writeTCP(payload)
}

// getDeviceInfo handles the request from Calibre for the device (that's us!)
// to send information about itself
func (c *calConn) getDeviceInfo(data map[string]interface{}) error {
	c.deviceInfo.DeviceVersion = c.clientOpts.DeviceModel
	c.deviceInfo.Version = "391"
	devInfoJSON, _ := json.Marshal(c.deviceInfo)
	payload := buildJSONpayload(devInfoJSON, OK)
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
		return err
	}
	decoder.Decode(data)
	if err != nil {
		return err
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
	payload := buildJSONpayload(fsJSON, OK)
	return c.writeTCP(payload)
}

// getBookCount sends Calibre a list of ebooks currently on the device.
// It is up to the client to decide how this list is derived
func (c *calConn) getBookCount(data map[string]interface{}) error {
	bc := BookCount{Count: c.ucdb.length(), WillStream: true, WillScan: true}
	if data["willUseCachedMetadata"].(bool) {
		bcJSON, _ := json.Marshal(bc)
		payload := buildJSONpayload(bcJSON, OK)
		// Send our count
		err := c.writeTCP(payload)
		if err != nil {
			return err
		}

		for _, b := range c.ucdb.booklist {
			bJSON, _ := json.Marshal(b)
			payload = buildJSONpayload(bJSON, OK)
			err := c.writeTCP(payload)
			if err != nil {
				return err
			}
		}
	} else {
		md := c.client.GetMetadataList([]BookID{})
		bc.Count = len(md)
		bcJSON, _ := json.Marshal(bc)
		payload := buildJSONpayload(bcJSON, OK)
		// Send our count
		err := c.writeTCP(payload)
		if err != nil {
			return err
		}
		for _, m := range md {
			mJSON, _ := json.Marshal(m)
			payload := buildJSONpayload(mJSON, OK)
			err := c.writeTCP(payload)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// resendMetadataList is called whenever using cached metadata, and
// Calibre requests a complete metadata listing (eg, when using a
// different Calibre library)
func (c *calConn) resendMetadataList(bookList []BookID) error {
	mdList := c.client.GetMetadataList(bookList)
	if len(mdList) == 0 {
		return c.writeTCP([]byte(c.okStr))
	}
	for _, md := range mdList {
		mJSON, _ := json.Marshal(md)
		payload := buildJSONpayload(mJSON, OK)
		err := c.writeTCP(payload)
		if err != nil {
			return err
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
			return errors.Wrap(err, "packet reading failed")
		}

		// Opcode should be SEND_BOOK_METADATA. If it's not, something
		// has gone rather wrong
		if opcode != SEND_BOOK_METADATA {
			return errors.New("unexpected calibre packet type")
		}
		err = mapstructure.Decode(newdata, &bkMD)
		if err != nil {
			return errors.Wrap(err, "unable to decode metadata packet")
		}
		md[i] = bkMD.Data
	}
	c.client.UpdateMetadata(md)
	return nil
}

// sendBook is where the magic starts to happen. It recieves one
// or more books from calibre.
func (c *calConn) sendBook(data map[string]interface{}) error {
	var bookDet SendBook
	err := mapstructure.Decode(data, &bookDet)
	if err != nil {
		return err
	}
	if bookDet.ThisBook == 0 {
		c.client.DisplayProgress(0)
	}
	lastBook := false
	if bookDet.ThisBook == (bookDet.TotalBooks - 1) {
		lastBook = true
	}
	w, err := c.client.SaveBook(bookDet.Metadata, lastBook)
	if err != nil {
		return err
	}
	if data["wantsSendOkToSendbook"].(bool) {
		c.writeTCP([]byte(c.okStr))
	}
	_, err = io.CopyN(w, c.tcpReader, int64(bookDet.Length))
	if err != nil {
		w.Close()
		return err
	}
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
	w.Close()
	c.ucdb.addEntry(bookDet.Metadata)
	c.client.DisplayProgress((bookDet.ThisBook * 100) / bookDet.TotalBooks)
	return nil
}

// deleteBook will delete any ebook Calibre tells us to
func (c *calConn) deleteBook(data map[string]interface{}) error {
	err := c.writeTCP([]byte(c.okStr))
	if err != nil {
		return err
	}
	var delBooks DeleteBooks
	mapstructure.Decode(data, &delBooks)
	for _, lp := range delBooks.Lpaths {
		_, bd, err := c.ucdb.find(Lpath, lp)
		if err != nil {
			return errors.New("lpath not in db to delete")
		}
		bID := BookID{Lpath: bd.Lpath, UUID: bd.UUID}
		err = c.client.DeleteBook(bID)
		if err != nil {
			return err
		}
		calConfirm, _ := json.Marshal(map[string]string{"uuid": bd.UUID})
		payload := buildJSONpayload(calConfirm, OK)
		c.writeTCP(payload)
		c.ucdb.removeEntry(Lpath, lp)
	}
	return nil
}

func (c *calConn) getBook(data map[string]interface{}) error {
	if !data["canStreamBinary"].(bool) || !data["canStream"].(bool) {
		return errors.New("calibre version does not support binary streaming")
	}
	lpath := data["lpath"].(string)
	filePos := int64(data["position"].(float64))
	_, bd, err := c.ucdb.find(Lpath, lpath)
	if err != nil {
		return errors.Wrap(err, "could not get book from db")
	}
	bID := BookID{Lpath: lpath, UUID: bd.UUID}
	bk, len, err := c.client.GetBook(bID, filePos)
	if err != nil {
		return errors.Wrap(err, "could not open book file")
	}
	gb := GetBook{
		WillStream:       true,
		WillStreamBinary: true,
		FileLength:       len,
	}
	gbJSON, _ := json.Marshal(gb)
	payload := buildJSONpayload(gbJSON, OK)
	c.writeTCP(payload)
	_, err = io.CopyN(c.tcpConn, bk, len)
	if err != nil {
		bk.Close()
		return errors.Wrap(err, "error sending book to Calibre")
	}
	bk.Close()
	c.tcpConn.SetDeadline(time.Now().Add(tcpDeadlineTimeout * time.Second))
	return nil
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
