package uc

import "encoding/json"

func (c *calConn) updateDB() {
	for i, b := range c.bookList {
		dbEnt := UncagedDB{Lpath: b.Lpath, UUID: b.UUID}
		c.db.Save(&dbEnt)
		c.db.One("UUID", b.UUID, &dbEnt)
		c.bookList[i].PriKey = dbEnt.PriKey
	}
}

func (c *calConn) prepareBookCountJSON() []byte {
	bc := BookCount{Count: len(c.bookList), WillStream: true, WillScan: true}
	jsonData, _ := json.Marshal(bc)
	return jsonData
}

func prepareBCdetail(bc BookCount) []byte {

	return nil
}
