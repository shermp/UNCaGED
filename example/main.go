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
	"log"
	"os"

	"github.com/shermp/UNCaGED/uncgd"
)

func main() {
	curDir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(curDir)
	var opts uncgd.ClientOptions
	opts.ClientName = "UNCaGED"
	opts.CoverDims.Height = 530
	opts.CoverDims.Width = 530
	opts.DeviceModel = "Win"
	opts.DeviceName = "UNCaGED Alpha"
	opts.DevStore.RootDir = curDir
	opts.DevStore.BookDir = "exampleBooks"
	opts.DevStore.LocationCode = "main"
	opts.DevStore.UUID = "498e8f45-b57f-4fb0-9cba-8c7dae1efb39"
	opts.SupportedExt = []string{"epub", "mobi"}

	c, err := uncgd.New(opts)
	cc := &c
	if err != nil {
		fmt.Print(err)
	} else {
		go cc.Listen()
	S:
		for {
			status := <-cc.Status
			switch status.StatCode {
			case uncgd.PrintMsg:
				fmt.Println(status.Value)
			case uncgd.TCPclosed:
				break S
			}
		}
	}
}
