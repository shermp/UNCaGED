package main

import (
	"fmt"
	"log"
	"os"

	"github.com/shermp/GoCalConn/calconn"
)

func main() {
	curDir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(curDir)
	var opts calconn.ClientOptions
	opts.ClientName = "Go Client"
	opts.CoverDims.Height = 530
	opts.CoverDims.Width = 530
	opts.DeviceModel = "Golang"
	opts.DeviceName = "Go Test"
	opts.DevStore.RootDir = curDir
	opts.DevStore.BookDir = "exampleBooks"
	opts.DevStore.LocationCode = "main"
	opts.DevStore.UUID = "498e8f45-b57f-4fb0-9cba-8c7dae1efb39"
	opts.SupportedExt = []string{"epub", "mobi"}

	c, err := calconn.New(opts)
	cc := &c
	if err != nil {
		fmt.Print(err)
	} else {
		go cc.Listen()
	S:
		for {
			status := <-cc.Status
			switch status.StatCode {
			case calconn.PrintMsg:
				fmt.Println(status.Value)
			case calconn.TCPclosed:
				break S
			}
		}
	}
}
